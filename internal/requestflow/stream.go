package requestflow

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/canonical"
	"github.com/luckymaomi/llmgateway/internal/resilience"
	"github.com/luckymaomi/llmgateway/internal/routing"
)

func (s *Service) Stream(ctx context.Context, command ChatCommand, sink StreamSink) *canonical.Error {
	if sink == nil {
		return &canonical.Error{Kind: canonical.ErrorInternalInvariant, Code: "missing_stream_sink", Message: "stream sink is required"}
	}
	run, prepareError := s.prepare(ctx, command)
	if prepareError != nil {
		return prepareError
	}
	if !run.request.Stream {
		_ = s.accounting.Release(ctx, run.accepted.RequestID, "invalid_request", "non-stream request used stream workflow")
		return &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "stream_not_enabled", Message: "stream must be true", Parameter: "stream", HTTPStatus: http.StatusBadRequest}
	}

	excluded := make([]routing.CandidateID, 0, len(run.candidates))
	startedAt := s.clock.Now().UTC()
	for attemptNumber := 1; ; attemptNumber++ {
		candidate, circuitPermit, selectionError := s.candidateDecision(run, excluded)
		if selectionError != nil {
			_ = s.accounting.Release(ctx, run.accepted.RequestID, selectionError.Code, selectionError.Message)
			return selectionError
		}
		committed, attemptError := s.streamAttempt(ctx, run, candidate, circuitPermit, attemptNumber, sink)
		if attemptError == nil {
			return nil
		}
		if committed || attemptError.Kind == canonical.ErrorUncertain || attemptError.Kind == canonical.ErrorStreamInterrupted {
			return attemptError
		}
		decision, err := s.retry.Decide(resilience.RetryInput{
			Attempt: attemptNumber, RequestStartedAt: startedAt, Failure: failureClass(attemptError.Kind),
			SendBoundary: resilience.SendRejected, ClientBoundary: resilience.ClientUncommitted,
			RetryAfter: retryAfter(attemptError.RetryAfter, s.clock.Now().UTC()),
		})
		if err != nil || decision.Action != resilience.RetrySchedule {
			_ = s.accounting.Release(ctx, run.accepted.RequestID, attemptError.Code, attemptError.Message)
			return attemptError
		}
		excluded = append(excluded, routing.CandidateID(candidate.ID.String()))
		if len(excluded) == len(run.candidates) {
			excluded = excluded[:0]
		}
		if err := waitUntil(ctx, decision.NextAttemptAt, s.clock.Now()); err != nil {
			_ = s.accounting.Release(context.WithoutCancel(ctx), run.accepted.RequestID, "canceled", "request canceled during retry wait")
			return &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "request_canceled", Message: "request was canceled", Cause: err}
		}
	}
}

func (s *Service) streamAttempt(ctx context.Context, run execution, candidate Candidate, circuitPermit *resilience.Permit, sequence int, sink StreamSink) (bool, *canonical.Error) {
	lease, _, err := s.coordinator.Acquire(ctx, LeaseRequest{
		RequestID: run.accepted.RequestID, UserID: run.command.Principal.UserID, GatewayKeyID: run.command.Principal.KeyID,
		ModelID: run.model.ID, ProviderID: run.model.ProviderID, CredentialID: candidate.ID, EstimatedTokens: run.estimatedTokens,
		RPMLimit: candidate.RPMLimit, TPMLimit: candidate.TPMLimit, Concurrency: candidate.ConcurrencyLimit,
	})
	if err != nil {
		circuitPermit.Complete(resilience.PermitReleased)
		return false, &canonical.Error{Kind: canonical.ErrorRateLimit, Code: "admission_unavailable", Message: "request capacity is temporarily unavailable", Cause: err}
	}
	defer func() { _ = lease.Release(context.WithoutCancel(ctx)) }()

	attemptID, err := s.repository.CreateAttempt(ctx, run.accepted.RequestID, candidate.ID, sequence)
	if err != nil {
		circuitPermit.Complete(resilience.PermitReleased)
		return false, storageError("attempt_create_failed", err)
	}
	adapter, client, request, buildError := s.buildUpstream(ctx, run, candidate)
	if buildError != nil {
		circuitPermit.Complete(resilience.PermitReleased)
		_ = s.failAttempt(context.WithoutCancel(ctx), attemptID, buildError, nil)
		return false, buildError
	}
	sentAt := s.clock.Now().UTC()
	if err := s.repository.UpdateAttempt(ctx, attemptID, AttemptUpdate{Status: "sending", SentAt: &sentAt}); err != nil {
		circuitPermit.Complete(resilience.PermitReleased)
		return false, storageError("attempt_state_failed", err)
	}
	response, err := client.Do(request)
	if err != nil {
		circuitPermit.Complete(resilience.PermitFailed)
		kind := string(canonical.ErrorUncertain)
		completedAt := s.clock.Now().UTC()
		_ = s.repository.UpdateAttempt(context.WithoutCancel(ctx), attemptID, AttemptUpdate{Status: "uncertain", ErrorKind: &kind, CompletedAt: &completedAt})
		usage := Usage{InputTokens: EstimateInputTokens(run.request), OutputTokens: estimateOutputBudget(run.request), Source: canonical.UsageEstimated}
		_ = s.accounting.Compensate(context.WithoutCancel(ctx), run.accepted.RequestID, usage, "upstream send outcome is uncertain")
		return false, &canonical.Error{Kind: canonical.ErrorUncertain, Code: "upstream_outcome_uncertain", Message: "upstream request outcome is uncertain", Cause: err}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		body, readError := readResponse(response, s.config.MaxResponseBytes)
		if readError != nil {
			circuitPermit.Complete(resilience.PermitFailed)
			providerError := &canonical.Error{Kind: canonical.ErrorProviderTemporary, Code: "provider_response_read_failed", Message: "provider response could not be read", Cause: readError}
			_ = s.failAttempt(context.WithoutCancel(ctx), attemptID, providerError, &response.StatusCode)
			return false, providerError
		}
		providerError := adapter.ClassifyError(response.StatusCode, response.Header, body)
		if tripsCircuit(providerError.Kind) {
			circuitPermit.Complete(resilience.PermitFailed)
		} else {
			circuitPermit.Complete(resilience.PermitReleased)
		}
		_ = s.failAttempt(context.WithoutCancel(ctx), attemptID, providerError, &response.StatusCode)
		return false, providerError
	}

	parser := adapter.ParseStream()
	buffer := make([]byte, 32<<10)
	state := streamState{inputTokens: EstimateInputTokens(run.request), usageSource: canonical.UsageEstimated}
	for {
		read, readError := response.Body.Read(buffer)
		if read > 0 {
			events, parseError := parser.Feed(buffer[:read])
			if parseError != nil {
				return s.finishBrokenStream(ctx, run, attemptID, circuitPermit, state, asCanonical(parseError, "malformed_provider_stream"))
			}
			if streamError := s.emitEvents(ctx, run.accepted.RequestID, attemptID, response.StatusCode, &state, events, sink); streamError != nil {
				return s.finishBrokenStream(ctx, run, attemptID, circuitPermit, state, streamError)
			}
		}
		if errors.Is(readError, io.EOF) {
			break
		}
		if readError != nil {
			providerError := &canonical.Error{Kind: canonical.ErrorStreamInterrupted, Code: "provider_stream_read_failed", Message: "provider stream was interrupted", Cause: readError}
			return s.finishBrokenStream(ctx, run, attemptID, circuitPermit, state, providerError)
		}
	}
	closingEvents, err := parser.Close()
	if err != nil {
		return s.finishBrokenStream(ctx, run, attemptID, circuitPermit, state, asCanonical(err, "malformed_provider_stream"))
	}
	if streamError := s.emitEvents(ctx, run.accepted.RequestID, attemptID, response.StatusCode, &state, closingEvents, sink); streamError != nil {
		return s.finishBrokenStream(ctx, run, attemptID, circuitPermit, state, streamError)
	}
	if !state.done {
		providerError := &canonical.Error{Kind: canonical.ErrorStreamInterrupted, Code: "provider_stream_incomplete", Message: "provider stream ended without completion"}
		return s.finishBrokenStream(ctx, run, attemptID, circuitPermit, state, providerError)
	}
	completedAt := s.clock.Now().UTC()
	status := response.StatusCode
	if err := s.repository.UpdateAttempt(ctx, attemptID, AttemptUpdate{Status: "completed", HTTPStatus: &status, FirstByteAt: state.firstByteAt, CompletedAt: &completedAt}); err != nil {
		_ = s.accounting.Compensate(context.WithoutCancel(ctx), run.accepted.RequestID, state.usage(), "stream completion could not be persisted")
		circuitPermit.Complete(resilience.PermitReleased)
		return state.committed, storageError("attempt_completion_failed", err)
	}
	if err := s.accounting.Settle(ctx, run.accepted.RequestID, state.usage()); err != nil {
		circuitPermit.Complete(resilience.PermitReleased)
		return state.committed, storageError("usage_settlement_failed", err)
	}
	circuitPermit.Complete(resilience.PermitSucceeded)
	if err := sink(run.accepted.RequestID, state.doneEvent); err != nil {
		return true, &canonical.Error{Kind: canonical.ErrorStreamInterrupted, Code: "client_stream_interrupted", Message: "client stopped receiving the stream completion", Cause: err}
	}
	return state.committed, nil
}

type streamState struct {
	committed    bool
	done         bool
	firstByteAt  *time.Time
	inputTokens  int64
	outputTokens int64
	usageSource  canonical.UsageSource
	doneEvent    canonical.StreamEvent
}

func (s *Service) emitEvents(ctx context.Context, requestID, attemptID uuid.UUID, status int, state *streamState, events []canonical.StreamEvent, sink StreamSink) *canonical.Error {
	for _, event := range events {
		if event.Type == canonical.StreamError {
			if event.Error != nil {
				return event.Error
			}
			return &canonical.Error{Kind: canonical.ErrorStreamInterrupted, Code: "provider_stream_error", Message: "provider stream failed"}
		}
		if !state.committed {
			now := s.clock.Now().UTC()
			if err := s.repository.UpdateAttempt(ctx, attemptID, AttemptUpdate{Status: "streaming", HTTPStatus: &status, FirstByteAt: &now}); err != nil {
				return storageError("stream_commit_failed", err)
			}
			state.firstByteAt = &now
			state.committed = true
		}
		if event.Usage != nil {
			if event.Usage.InputTokens != nil {
				state.inputTokens = *event.Usage.InputTokens
			}
			if event.Usage.OutputTokens != nil {
				state.outputTokens = *event.Usage.OutputTokens
			}
			state.usageSource = event.Usage.Source
		} else {
			state.outputTokens += EstimatedOutputTokens([]canonical.StreamEvent{event})
		}
		if event.Type == canonical.StreamDone {
			state.done = true
			state.doneEvent = event
			continue
		}
		if err := sink(requestID, event); err != nil {
			return &canonical.Error{Kind: canonical.ErrorStreamInterrupted, Code: "client_stream_interrupted", Message: "client stopped receiving the stream", Cause: err}
		}
	}
	return nil
}

func (s *Service) finishBrokenStream(ctx context.Context, run execution, attemptID uuid.UUID, permit *resilience.Permit, state streamState, providerError *canonical.Error) (bool, *canonical.Error) {
	permit.Complete(resilience.PermitFailed)
	status := "failed"
	if state.committed {
		status = "uncertain"
		providerError.Kind = canonical.ErrorStreamInterrupted
	}
	kind := string(providerError.Kind)
	completedAt := s.clock.Now().UTC()
	_ = s.repository.UpdateAttempt(context.WithoutCancel(ctx), attemptID, AttemptUpdate{Status: status, ErrorKind: &kind, FirstByteAt: state.firstByteAt, CompletedAt: &completedAt})
	if state.committed {
		_ = s.accounting.Compensate(context.WithoutCancel(ctx), run.accepted.RequestID, state.usage(), providerError.Message)
	}
	return state.committed, providerError
}

func (state streamState) usage() Usage {
	return Usage{InputTokens: state.inputTokens, OutputTokens: state.outputTokens, Source: state.usageSource}
}
