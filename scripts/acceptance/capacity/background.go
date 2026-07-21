package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
)

func (c *loadClient) background(ctx context.Context, phase string, user virtualUser) requestResult {
	result := requestResult{Phase: phase, Kind: kindBackground, UserID: user.ID}
	body, _ := json.Marshal(map[string]any{
		"model": c.model, "input": "capacity background", "max_output_tokens": 16, "store": true, "background": true,
	})
	baseURL := c.baseURLs[int(c.nextHost.Add(1)-1)%len(c.baseURLs)]
	requestContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(requestContext, http.MethodPost, baseURL+"/v1/responses", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+user.Secret)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", uuid.NewString())
	started := time.Now()
	response, err := c.client.Do(request)
	result.FirstByte = time.Since(started)
	if err != nil {
		result.Failure = "transport_" + errorClass(err)
		result.Latency = time.Since(started)
		return result
	}
	payload, readErr := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	response.Body.Close()
	result.Status = response.StatusCode
	if readErr != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		result.Failure = "background_create"
		result.ErrorCode = responseErrorCode(payload)
		result.Latency = time.Since(started)
		return result
	}
	var created struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(payload, &created) != nil || created.ID == "" {
		result.Failure = "background_create_shape"
		result.Latency = time.Since(started)
		return result
	}
	for {
		if time.Since(started) > 15*time.Second {
			result.Failure = "background_poll_timeout"
			break
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-requestContext.Done():
			timer.Stop()
			result.Failure = "background_context"
			result.Latency = time.Since(started)
			return result
		case <-timer.C:
		}
		poll, _ := http.NewRequestWithContext(requestContext, http.MethodGet, baseURL+"/v1/responses/"+created.ID, nil)
		poll.Header.Set("Authorization", "Bearer "+user.Secret)
		pollResponse, err := c.client.Do(poll)
		if err != nil {
			result.Failure = "background_poll_" + errorClass(err)
			break
		}
		pollPayload, readErr := io.ReadAll(io.LimitReader(pollResponse.Body, 8<<20))
		pollResponse.Body.Close()
		if readErr != nil || pollResponse.StatusCode != http.StatusOK {
			result.Failure = "background_poll_response"
			break
		}
		var state struct {
			Status string `json:"status"`
		}
		if json.Unmarshal(pollPayload, &state) != nil {
			result.Failure = "background_poll_shape"
			break
		}
		if state.Status == "completed" {
			result.Completed = true
			break
		}
		if state.Status == "failed" || state.Status == "canceled" || state.Status == "uncertain" {
			result.Failure = "background_" + state.Status
			break
		}
	}
	result.Latency = time.Since(started)
	return result
}
