package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

type requestKind string

const (
	kindShort          requestKind = "short_chat"
	kindShortStream    requestKind = "short_stream"
	kindLongStream     requestKind = "long_stream"
	kindExtendedStream requestKind = "extended_stream"
	kindToolReason     requestKind = "tool_reasoning"
	kindBackground     requestKind = "background_response"
)

type requestResult struct {
	Phase          string
	Kind           requestKind
	UserID         uuid.UUID
	Status         int
	Latency        time.Duration
	FirstByte      time.Duration
	RetryAfter     bool
	Completed      bool
	ErrorCode      string
	Failure        string
	IdempotencyKey string
	DialRetries    int
}

type loadClient struct {
	baseURLs []string
	model    string
	client   *http.Client
	nextHost atomic.Uint64
}

func newLoadClient(baseURLs []string, model string) *loadClient {
	transport := &http.Transport{
		MaxIdleConns: 1024, MaxIdleConnsPerHost: 512, MaxConnsPerHost: 512,
		IdleConnTimeout: 90 * time.Second, ResponseHeaderTimeout: 30 * time.Second,
	}
	return &loadClient{baseURLs: baseURLs, model: model, client: &http.Client{Transport: transport}}
}

func (c *loadClient) close() {
	if transport, ok := c.client.Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
}

func (c *loadClient) execute(ctx context.Context, phase string, kind requestKind, user virtualUser) requestResult {
	if kind == kindBackground {
		return c.background(ctx, phase, user)
	}
	prompt := map[requestKind]string{
		kindShort: "capacity short", kindShortStream: "capacity short stream",
		kindLongStream: "capacity long stream", kindExtendedStream: "capacity extended stream", kindToolReason: "capacity tool reasoning",
	}[kind]
	body := map[string]any{
		"model": c.model, "messages": []map[string]string{{"role": "user", "content": prompt}},
		"max_tokens": 16, "stream": kind == kindShortStream || kind == kindLongStream || kind == kindExtendedStream,
	}
	if kind == kindToolReason {
		body["thinking"] = map[string]string{"type": "enabled"}
		body["tools"] = []any{map[string]any{"type": "function", "function": map[string]any{
			"name": "capacity_probe", "description": "Return a stable capacity probe value",
			"parameters": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
		}}}
	}
	return c.send(ctx, phase, kind, user, "/v1/chat/completions", body, kind == kindShortStream || kind == kindLongStream || kind == kindExtendedStream)
}

func (c *loadClient) send(ctx context.Context, phase string, kind requestKind, user virtualUser, path string, body any, stream bool) requestResult {
	result := requestResult{Phase: phase, Kind: kind, UserID: user.ID}
	encoded, err := json.Marshal(body)
	if err != nil {
		result.Failure = "encode_request"
		return result
	}
	requestContext, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	result.IdempotencyKey = uuid.NewString()
	started := time.Now()
	var response *http.Response
	for attempt := 0; attempt < 2; attempt++ {
		baseURL := c.baseURLs[int(c.nextHost.Add(1)-1)%len(c.baseURLs)]
		request, requestErr := http.NewRequestWithContext(requestContext, http.MethodPost, baseURL+path, bytes.NewReader(encoded))
		if requestErr != nil {
			result.Failure = "create_request"
			return result
		}
		request.Header.Set("Authorization", "Bearer "+user.Secret)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", result.IdempotencyKey)
		response, err = c.client.Do(request)
		if err == nil {
			break
		}
		if attempt == 0 && requestContext.Err() == nil && isSafeDialFailure(err) {
			result.DialRetries++
			continue
		}
		result.Latency = time.Since(started)
		result.Failure = "transport_" + errorClass(err)
		return result
	}
	result.FirstByte = time.Since(started)
	defer response.Body.Close()
	result.Status = response.StatusCode
	result.RetryAfter = retryAfterSeconds(response.Header.Get("Retry-After"))
	if stream && response.StatusCode >= 200 && response.StatusCode < 300 {
		scanner := bufio.NewScanner(io.LimitReader(response.Body, 8<<20))
		scanner.Buffer(make([]byte, 64<<10), 1<<20)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "data: [DONE]" {
				result.Completed = true
			}
			if strings.HasPrefix(line, "data:") && strings.Contains(line, `"error":`) {
				result.Failure = "stream_error_event"
			}
		}
		if err := scanner.Err(); err != nil {
			result.Failure = "stream_" + errorClass(err)
		}
	} else {
		payload, readErr := io.ReadAll(io.LimitReader(response.Body, 8<<20))
		if readErr != nil {
			result.Failure = "body_" + errorClass(readErr)
		} else if response.StatusCode >= 200 && response.StatusCode < 300 {
			result.Completed = true
		} else {
			result.ErrorCode = responseErrorCode(payload)
		}
	}
	result.Latency = time.Since(started)
	return result
}

func isSafeDialFailure(err error) bool {
	var networkError *net.OpError
	return errors.As(err, &networkError) && networkError.Op == "dial"
}

func responseErrorCode(payload []byte) string {
	var value struct {
		Error struct {
			Code any `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(payload, &value) != nil || value.Error.Code == nil {
		return ""
	}
	return fmt.Sprint(value.Error.Code)
}

func errorClass(err error) string {
	var networkError *net.OpError
	if errors.As(err, &networkError) {
		return "net_" + networkError.Op
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	name := fmt.Sprintf("%T", err)
	if index := strings.LastIndex(name, "."); index >= 0 {
		name = name[index+1:]
	}
	return strings.TrimPrefix(name, "*")
}

type resultCollector struct {
	mu      sync.Mutex
	results []requestResult
}

func (c *resultCollector) add(result requestResult) {
	c.mu.Lock()
	c.results = append(c.results, result)
	c.mu.Unlock()
}

func runConcurrent(ctx context.Context, client *loadClient, collector *resultCollector, phase string, users []virtualUser, kind func(int) requestKind) {
	var group sync.WaitGroup
	for index, user := range users {
		group.Add(1)
		go func(index int, user virtualUser) {
			defer group.Done()
			collector.add(client.execute(ctx, phase, kind(index), user))
		}(index, user)
	}
	group.Wait()
}

func runSteady(ctx context.Context, client *loadClient, collector *resultCollector, users []virtualUser, duration time.Duration) {
	deadline := time.Now().Add(duration)
	var group sync.WaitGroup
	for index, user := range users {
		group.Add(1)
		go func(offset int, user virtualUser) {
			defer group.Done()
			sequence := offset
			for time.Now().Before(deadline) && ctx.Err() == nil {
				collector.add(client.execute(ctx, "steady", mixedKind(sequence), user))
				sequence++
				remaining := time.Until(deadline)
				if remaining <= 0 {
					return
				}
				wait := min(500*time.Millisecond, remaining)
				timer := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}(index, user)
	}
	group.Wait()
}

func mixedKind(sequence int) requestKind {
	switch sequence % 20 {
	case 0:
		return kindBackground
	case 1, 2:
		return kindLongStream
	case 3, 4, 5:
		return kindShortStream
	case 6:
		return kindToolReason
	default:
		return kindShort
	}
}

func retryAfterSeconds(value string) bool {
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
		return seconds >= 0
	}
	if retryAt, err := http.ParseTime(strings.TrimSpace(value)); err == nil {
		return retryAt.After(time.Now().Add(-time.Second))
	}
	return false
}
