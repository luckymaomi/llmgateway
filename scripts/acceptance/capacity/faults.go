package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type faultReport struct {
	Name             string            `json:"name"`
	Status           int               `json:"status"`
	ErrorCode        string            `json:"errorCode,omitempty"`
	RetryAfter       bool              `json:"retryAfter"`
	Completed        bool              `json:"completed"`
	ClientFailure    string            `json:"clientFailure,omitempty"`
	ProviderRequests int64             `json:"providerRequests"`
	Database         faultDatabaseFact `json:"database"`
}

func runFaults(ctx context.Context, client *loadClient, pool *pgxpool.Pool, user virtualUser, providerAdminURL string) ([]faultReport, error) {
	type scenario struct {
		name    string
		execute func() requestResult
	}
	chat := func(prompt string, stream bool) requestResult {
		return client.send(ctx, "fault", kindShort, user, "/v1/chat/completions", map[string]any{
			"model": client.model, "messages": []map[string]string{{"role": "user", "content": prompt}},
			"max_tokens": 16, "stream": stream,
		}, stream)
	}
	scenarios := []scenario{
		{name: "context_rejected_before_send", execute: func() requestResult {
			return chat(strings.Repeat("capacity context ", 10000), false)
		}},
		{name: "client_cancel_after_first_event", execute: func() requestResult {
			cancelContext, cancel := context.WithCancel(ctx)
			defer cancel()
			result := make(chan requestResult, 1)
			go func() {
				result <- client.send(cancelContext, "fault", kindLongStream, user, "/v1/chat/completions", map[string]any{
					"model": client.model, "messages": []map[string]string{{"role": "user", "content": "capacity long stream"}},
					"max_tokens": 16, "stream": true,
				}, true)
			}()
			timer := time.NewTimer(250 * time.Millisecond)
			select {
			case value := <-result:
				timer.Stop()
				return value
			case <-timer.C:
				cancel()
				return <-result
			}
		}},
		{name: "rate_limit_bounded_switch", execute: func() requestResult { return chat("capacity rate limit once", false) }},
		{name: "generic_503_uncertain", execute: func() requestResult { return chat("capacity provider 503", false) }},
		{name: "malformed_stream_after_commit", execute: func() requestResult { return chat("capacity malformed stream", true) }},
		{name: "transport_disconnect_uncertain", execute: func() requestResult { return chat("capacity transport disconnect", false) }},
	}
	reports := make([]faultReport, 0, len(scenarios))
	for _, scenario := range scenarios {
		before, err := readProviderStats(ctx, providerAdminURL)
		if err != nil {
			return nil, err
		}
		result := scenario.execute()
		fact, err := waitForFaultDatabaseFact(ctx, pool, result.IdempotencyKey, scenario.name == "context_rejected_before_send")
		if err != nil {
			return nil, err
		}
		after, err := readProviderStats(ctx, providerAdminURL)
		if err != nil {
			return nil, err
		}
		reports = append(reports, faultReport{
			Name: scenario.name, Status: result.Status, ErrorCode: result.ErrorCode, RetryAfter: result.RetryAfter,
			Completed: result.Completed, ClientFailure: result.Failure,
			ProviderRequests: after["requests"] - before["requests"], Database: fact,
		})
	}
	return reports, nil
}

func waitForFaultDatabaseFact(ctx context.Context, pool *pgxpool.Pool, idempotencyKey string, expectMissing bool) (faultDatabaseFact, error) {
	deadline := time.Now().Add(3 * time.Second)
	for {
		fact, err := readFaultDatabaseFact(ctx, pool, idempotencyKey)
		if err != nil {
			return fact, err
		}
		if expectMissing || fact.Found && fact.Request != "dispatching" && fact.Request != "streaming" && fact.Request != "queued" {
			return fact, nil
		}
		if time.Now().After(deadline) {
			return fact, fmt.Errorf("fault request %q did not reach a durable terminal state", idempotencyKey)
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fact, ctx.Err()
		case <-timer.C:
		}
	}
}

func validateFaults(faults []faultReport) []string {
	expected := map[string]struct {
		status, attempts           int64
		code, request, reservation string
		providerRequests           int64
		retry, found               bool
	}{
		"context_rejected_before_send":    {status: 400, code: "context_length_exceeded", providerRequests: 0},
		"client_cancel_after_first_event": {status: 200, attempts: 1, request: "uncertain", reservation: "reserved", providerRequests: 1, found: true},
		"rate_limit_bounded_switch":       {status: 200, attempts: 2, request: "completed", reservation: "settled", providerRequests: 2, found: true},
		"generic_503_uncertain":           {status: 409, attempts: 1, code: "upstream_outcome_uncertain", request: "uncertain", reservation: "reserved", providerRequests: 1, retry: true, found: true},
		"malformed_stream_after_commit":   {status: 200, attempts: 1, request: "uncertain", reservation: "reserved", providerRequests: 1, found: true},
		"transport_disconnect_uncertain":  {status: 409, attempts: 1, code: "upstream_outcome_uncertain", request: "uncertain", reservation: "reserved", providerRequests: 1, retry: true, found: true},
	}
	var failures []string
	for _, fault := range faults {
		want, exists := expected[fault.Name]
		if !exists || int64(fault.Status) != want.status || fault.ErrorCode != want.code || fault.RetryAfter != want.retry ||
			fault.ProviderRequests != want.providerRequests || fault.Database.Found != want.found || fault.Database.Attempts != want.attempts ||
			fault.Database.Request != want.request || fault.Database.Reservation != want.reservation {
			failures = append(failures, fault.Name+": fault contract drifted")
		}
		if fault.Name == "rate_limit_bounded_switch" && (!fault.Completed || fault.ClientFailure != "") {
			failures = append(failures, fault.Name+": bounded switch did not complete")
		}
		if fault.Name == "malformed_stream_after_commit" && fault.ClientFailure != "stream_error_event" {
			failures = append(failures, fault.Name+": client did not observe the committed stream error")
		}
	}
	return failures
}
