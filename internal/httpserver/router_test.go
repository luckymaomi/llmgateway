package httpserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/luckymaomi/llmgateway/internal/config"
	"github.com/prometheus/client_golang/prometheus"
)

type readinessStub struct {
	err error
}

func (s readinessStub) Ready(context.Context) error { return s.err }

func TestHealthEndpointsExposeRuntimeState(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{HTTP: config.HTTP{MaxBodyBytes: 1024}}
	router := NewRouter(cfg, logger, readinessStub{}, prometheus.NewRegistry(), nil, nil)

	for _, path := range []string{"/health/live", "/health/ready"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s returned %d", path, response.Code)
		}
		if response.Header().Get("X-Request-ID") == "" {
			t.Fatalf("%s did not expose a request ID", path)
		}
	}
}

func TestReadinessReportsDependencyFailure(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{HTTP: config.HTTP{MaxBodyBytes: 1024}}
	router := NewRouter(cfg, logger, readinessStub{err: errors.New("database offline")}, prometheus.NewRegistry(), nil, nil)

	request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready returned %d", response.Code)
	}
}

func TestRequestIDRejectsUnsafeInput(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{HTTP: config.HTTP{MaxBodyBytes: 1024}}
	router := NewRouter(cfg, logger, readinessStub{}, prometheus.NewRegistry(), nil, nil)

	request := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	request.Header.Set("X-Request-ID", "unsafe\nvalue")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Header().Get("X-Request-ID") == "unsafe\nvalue" {
		t.Fatal("unsafe request ID was reflected")
	}
}
