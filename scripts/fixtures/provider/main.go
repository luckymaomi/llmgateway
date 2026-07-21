package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type fixture struct {
	releaseOnce sync.Once
	release     chan struct{}
	held        atomic.Int64
	active      atomic.Int64
	completed   atomic.Int64
	canceled    atomic.Int64
	rateLimited atomic.Int64
}

func main() {
	address := flag.String("address", "127.0.0.1:18443", "TLS Provider address")
	adminAddress := flag.String("admin-address", "127.0.0.1:18444", "fixture control address")
	certificatePath := flag.String("certificate-out", "", "path for the fixture CA certificate")
	certificateIP := flag.String("certificate-ip", "", "IP address included in the Provider certificate")
	flag.Parse()
	if *certificatePath == "" || net.ParseIP(*certificateIP) == nil {
		panic("certificate-out and a valid certificate-ip are required")
	}
	certificate, certificatePEM, err := newCertificate(net.ParseIP(*certificateIP))
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(*certificatePath, certificatePEM, 0o600); err != nil {
		panic(err)
	}

	state := &fixture{release: make(chan struct{})}
	provider := &http.Server{
		Addr:         *address,
		Handler:      state.providerRoutes(),
		TLSConfig:    &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12},
		TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){},
	}
	admin := &http.Server{Addr: *adminAddress, Handler: state.adminRoutes()}
	errors := make(chan error, 2)
	go func() { errors <- provider.ListenAndServeTLS("", "") }()
	go func() { errors <- admin.ListenAndServe() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case <-ctx.Done():
	case err := <-errors:
		if err != nil && err != http.ErrServerClosed {
			panic(err)
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = provider.Shutdown(shutdown)
	_ = admin.Shutdown(shutdown)
}

func (f *fixture) providerRoutes() http.Handler {
	router := http.NewServeMux()
	router.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer core-upstream-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   []any{map[string]any{"id": "fixture-chat", "object": "model"}},
		})
	})
	router.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer core-upstream-secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		body := http.MaxBytesReader(w, r.Body, 1<<20)
		payload, err := readAll(body)
		if err != nil || !bytes.Contains(payload, []byte(`"model":"fixture-chat"`)) {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if bytes.Contains(payload, []byte("continue stored response")) &&
			(!bytes.Contains(payload, []byte("hello from the stored Responses flow")) || !bytes.Contains(payload, []byte("fixture response"))) {
			http.Error(w, "previous response history is missing", http.StatusBadRequest)
			return
		}
		if bytes.Contains(payload, []byte("drop after read")) {
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				http.Error(w, "fixture connection cannot be terminated", http.StatusInternalServerError)
				return
			}
			connection, _, hijackError := hijacker.Hijack()
			if hijackError != nil {
				http.Error(w, "could not terminate fixture connection", http.StatusInternalServerError)
				return
			}
			_ = connection.Close()
			return
		}
		if bytes.Contains(payload, []byte("persist credential cooldown")) {
			f.rateLimited.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"type": "rate_limit_error", "code": "fixture_rate_limit", "message": "fixture credential is rate limited"}})
			return
		}
		if bytes.Contains(payload, []byte("hold capacity")) || bytes.Contains(payload, []byte("hold background")) {
			f.held.Add(1)
			f.active.Add(1)
			defer f.active.Add(-1)
			select {
			case <-f.release:
			case <-r.Context().Done():
				f.canceled.Add(1)
				return
			}
		}
		var requestShape struct {
			Stream bool `json:"stream"`
		}
		if json.Unmarshal(payload, &requestShape) == nil && requestShape.Stream {
			f.streamResponse(w, r, bytes.Contains(payload, []byte("hold stream")))
			return
		}
		f.completed.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-fixture", "model": "fixture-chat", "created": time.Now().Unix(),
			"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "fixture response"}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 4, "completion_tokens": 2, "total_tokens": 6},
		})
	})
	return router
}

func (f *fixture) streamResponse(w http.ResponseWriter, r *http.Request, hold bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming is unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	write := func(value string) error {
		if _, err := io.WriteString(w, "data: "+value+"\n\n"); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	if err := write(`{"id":"fixture-stream","request_id":"fixture-request","created":1710000100,"model":"fixture-chat","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`); err != nil {
		return
	}
	if err := write(`{"id":"fixture-stream","request_id":"fixture-request","created":1710000100,"model":"fixture-chat","choices":[{"index":0,"delta":{"content":"fixture stream"},"finish_reason":null}]}`); err != nil {
		return
	}
	if hold {
		f.held.Add(1)
		select {
		case <-f.release:
		case <-r.Context().Done():
			f.canceled.Add(1)
			return
		}
	}
	if err := write(`{"id":"fixture-stream","request_id":"fixture-request","created":1710000100,"model":"fixture-chat","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`); err != nil {
		return
	}
	if err := write("[DONE]"); err != nil {
		return
	}
	f.completed.Add(1)
}

func (f *fixture) adminRoutes() http.Handler {
	router := http.NewServeMux()
	router.HandleFunc("/stats", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]int64{
			"held": f.held.Load(), "active": f.active.Load(), "completed": f.completed.Load(),
			"canceled": f.canceled.Load(), "rate_limited": f.rateLimited.Load(),
		})
	})
	router.HandleFunc("/release", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		f.releaseOnce.Do(func() { close(f.release) })
		w.WriteHeader(http.StatusNoContent)
	})
	return router
}

func newCertificate(address net.IP) (tls.Certificate, []byte, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	template := x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: "LLMGateway test Provider"},
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, IsCA: true, IPAddresses: []net.IP{address},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	certificate, err := tls.X509KeyPair(certificatePEM, keyPEM)
	return certificate, certificatePEM, err
}

func readAll(body io.Reader) ([]byte, error) {
	payload, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("read request: %w", err)
	}
	return payload, nil
}
