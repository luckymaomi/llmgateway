package main

import (
	"io"
	"net/http"
	"time"
)

func (f *fixture) streamResponse(w http.ResponseWriter, r *http.Request, hold, long, extended, malformed bool) {
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
	chunks := 1
	delay := time.Duration(0)
	if extended {
		chunks = 600
		delay = 50 * time.Millisecond
	} else if long {
		chunks = 20
		delay = 50 * time.Millisecond
	}
	for range chunks {
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				f.canceled.Add(1)
				return
			}
		}
		if err := write(`{"id":"fixture-stream","request_id":"fixture-request","created":1710000100,"model":"fixture-chat","choices":[{"index":0,"delta":{"content":"fixture stream"},"finish_reason":null}]}`); err != nil {
			return
		}
	}
	if malformed {
		f.malformed.Add(1)
		_ = write(`{"id":`)
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
