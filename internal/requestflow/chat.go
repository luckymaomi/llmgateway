package requestflow

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/canonical"
	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/resilience"
	"github.com/luckymaomi/llmgateway/internal/routing"
)

func (s *Service) Chat(ctx context.Context, command ChatCommand) (ChatResult, *canonical.Error) {
	run, prepareError := s.prepare(ctx, command)
	if prepareError != nil {
		return ChatResult{}, prepareError
	}
	if run.request.Stream {
		_ = s.accounting.Release(ctx, run.accepted.RequestID, "invalid_request", "stream request used non-stream workflow")
		return ChatResult{}, &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "stream_requires_stream_endpoint", Message: "streaming request requires a stream sink", Parameter: "stream", HTTPStatus: http.StatusBadRequest}
	}

	excluded := make([]routing.CandidateID, 0, len(run.candidates))
	startedAt := s.clock.Now().UTC()
	for attemptNumber := 1; ; attemptNumber++ {
		candidate, circuitPermit, selectionError := s.candidateDecision(run, excluded)
		if selectionError != nil {
			_ = s.accounting.Release(ctx, run.accepted.RequestID, selectionError.Code, selectionError.Message)
			return ChatResult{}, selectionError
		}
		result, attemptError := s.nonStreamAttempt(ctx, run, candidate, circuitPermit, attemptNumber)
		if attemptError == nil {
			return result, nil
		}
		if attemptError.Kind == canonical.ErrorUncertain {
			return ChatResult{}, attemptError
		}
		decision, err := s.retry.Decide(resilience.RetryInput{
			Attempt: attemptNumber, RequestStartedAt: startedAt, Failure: failureClass(attemptError.Kind),
			SendBoundary: resilience.SendRejected, ClientBoundary: resilience.ClientUncommitted,
			RetryAfter: retryAfter(attemptError.RetryAfter, s.clock.Now().UTC()),
		})
		if err != nil || decision.Action != resilience.RetrySchedule {
			_ = s.accounting.Release(ctx, run.accepted.RequestID, attemptError.Code, attemptError.Message)
			return ChatResult{}, attemptError
		}
		excluded = append(excluded, routing.CandidateID(candidate.ID.String()))
		if len(excluded) == len(run.candidates) {
			excluded = excluded[:0]
		}
		if err := waitUntil(ctx, decision.NextAttemptAt, s.clock.Now()); err != nil {
			_ = s.accounting.Release(context.WithoutCancel(ctx), run.accepted.RequestID, "canceled", "request canceled during retry wait")
			return ChatResult{}, &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "request_canceled", Message: "request was canceled", Cause: err}
		}
	}
}

func (s *Service) nonStreamAttempt(ctx context.Context, run execution, candidate Candidate, circuitPermit *resilience.Permit, sequence int) (ChatResult, *canonical.Error) {
	lease, _, err := s.coordinator.Acquire(ctx, LeaseRequest{
		RequestID: run.accepted.RequestID, UserID: run.command.Principal.UserID, GatewayKeyID: run.command.Principal.KeyID,
		ModelID: run.model.ID, ProviderID: run.model.ProviderID, CredentialID: candidate.ID, EstimatedTokens: run.estimatedTokens,
		RPMLimit: candidate.RPMLimit, TPMLimit: candidate.TPMLimit, Concurrency: candidate.ConcurrencyLimit,
	})
	if err != nil {
		circuitPermit.Complete(resilience.PermitReleased)
		return ChatResult{}, &canonical.Error{Kind: canonical.ErrorRateLimit, Code: "admission_unavailable", Message: "request capacity is temporarily unavailable", Cause: err}
	}
	defer func() { _ = lease.Release(context.WithoutCancel(ctx)) }()

	attemptID, err := s.repository.CreateAttempt(ctx, run.accepted.RequestID, candidate.ID, sequence)
	if err != nil {
		circuitPermit.Complete(resilience.PermitReleased)
		return ChatResult{}, storageError("attempt_create_failed", err)
	}
	adapter, client, request, buildError := s.buildUpstream(ctx, run, candidate)
	if buildError != nil {
		circuitPermit.Complete(resilience.PermitReleased)
		_ = s.failAttempt(context.WithoutCancel(ctx), attemptID, buildError, nil)
		return ChatResult{}, buildError
	}
	sentAt := s.clock.Now().UTC()
	if err := s.repository.UpdateAttempt(ctx, attemptID, AttemptUpdate{Status: "sending", SentAt: &sentAt}); err != nil {
		circuitPermit.Complete(resilience.PermitReleased)
		return ChatResult{}, storageError("attempt_state_failed", err)
	}
	response, err := client.Do(request)
	if err != nil {
		circuitPermit.Complete(resilience.PermitFailed)
		kind := string(canonical.ErrorUncertain)
		completedAt := s.clock.Now().UTC()
		_ = s.repository.UpdateAttempt(context.WithoutCancel(ctx), attemptID, AttemptUpdate{Status: "uncertain", ErrorKind: &kind, CompletedAt: &completedAt})
		usage := Usage{InputTokens: EstimateInputTokens(run.request), OutputTokens: estimateOutputBudget(run.request), Source: canonical.UsageEstimated}
		_ = s.accounting.Compensate(context.WithoutCancel(ctx), run.accepted.RequestID, usage, "upstream send outcome is uncertain")
		return ChatResult{}, &canonical.Error{Kind: canonical.ErrorUncertain, Code: "upstream_outcome_uncertain", Message: "upstream request outcome is uncertain", Cause: err}
	}
	defer response.Body.Close()
	body, readError := readResponse(response, s.config.MaxResponseBytes)
	if readError != nil {
		circuitPermit.Complete(resilience.PermitFailed)
		providerError := &canonical.Error{Kind: canonical.ErrorProviderTemporary, Code: "provider_response_read_failed", Message: "provider response could not be read", Cause: readError}
		_ = s.failAttempt(context.WithoutCancel(ctx), attemptID, providerError, &response.StatusCode)
		return ChatResult{}, providerError
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		providerError := adapter.ClassifyError(response.StatusCode, response.Header, body)
		if tripsCircuit(providerError.Kind) {
			circuitPermit.Complete(resilience.PermitFailed)
		} else {
			circuitPermit.Complete(resilience.PermitReleased)
		}
		_ = s.failAttempt(context.WithoutCancel(ctx), attemptID, providerError, &response.StatusCode)
		return ChatResult{}, providerError
	}
	parsed, err := adapter.ParseResponse(response.StatusCode, response.Header, body)
	if err != nil {
		circuitPermit.Complete(resilience.PermitFailed)
		providerError := asCanonical(err, "provider_contract_error")
		_ = s.failAttempt(context.WithoutCancel(ctx), attemptID, providerError, &response.StatusCode)
		return ChatResult{}, providerError
	}
	completedAt := s.clock.Now().UTC()
	status := response.StatusCode
	if err := s.repository.UpdateAttempt(ctx, attemptID, AttemptUpdate{Status: "completed", HTTPStatus: &status, UpstreamRequestID: optionalString(parsed.RequestID), FirstByteAt: &completedAt, CompletedAt: &completedAt}); err != nil {
		circuitPermit.Complete(resilience.PermitReleased)
		_ = s.accounting.Compensate(context.WithoutCancel(ctx), run.accepted.RequestID, responseUsage(run.request, parsed), "attempt completion could not be persisted")
		return ChatResult{}, storageError("attempt_completion_failed", err)
	}
	if err := s.accounting.Settle(ctx, run.accepted.RequestID, responseUsage(run.request, parsed)); err != nil {
		circuitPermit.Complete(resilience.PermitReleased)
		return ChatResult{}, storageError("usage_settlement_failed", err)
	}
	circuitPermit.Complete(resilience.PermitSucceeded)
	parsed.Model = run.model.PublicName
	return ChatResult{RequestID: run.accepted.RequestID, Response: parsed}, nil
}

func (s *Service) buildUpstream(ctx context.Context, run execution, candidate Candidate) (providers.Adapter, *http.Client, *http.Request, *canonical.Error) {
	adapter, err := s.factory.Adapter(run.model)
	if err != nil {
		return nil, nil, nil, &canonical.Error{Kind: canonical.ErrorProviderConfiguration, Code: "adapter_configuration", Message: "provider adapter is invalid", Cause: err}
	}
	secret, err := s.secrets.CredentialSecret(ctx, candidate.ID)
	if err != nil {
		return nil, nil, nil, &canonical.Error{Kind: canonical.ErrorProviderConfiguration, Code: "credential_decryption_failed", Message: "provider credential is unavailable", Cause: err}
	}
	request, err := adapter.BuildRequest(ctx, providerCredential(secret), run.request)
	secret = ""
	if err != nil {
		return nil, nil, nil, asCanonical(err, "provider_request_invalid")
	}
	client, err := s.factory.Client(candidate)
	if err != nil {
		return nil, nil, nil, &canonical.Error{Kind: canonical.ErrorProviderConfiguration, Code: "provider_transport_invalid", Message: "provider transport is unavailable", Cause: err}
	}
	return adapter, client, request, nil
}

func (s *Service) failAttempt(ctx context.Context, attemptID uuid.UUID, providerError *canonical.Error, status *int) error {
	completedAt := s.clock.Now().UTC()
	kind := string(providerError.Kind)
	return s.repository.UpdateAttempt(ctx, attemptID, AttemptUpdate{
		Status: "failed", HTTPStatus: status, ErrorKind: &kind,
		RetryAfterAt: retryAfterAt(providerError.RetryAfter, completedAt), CompletedAt: &completedAt,
	})
}

func responseUsage(request canonical.ChatRequest, response canonical.ChatResponse) Usage {
	usage := Usage{InputTokens: EstimateInputTokens(request), Source: canonical.UsageEstimated}
	for _, choice := range response.Choices {
		for _, part := range choice.Message.Content {
			usage.OutputTokens += int64(len(part.Text)+3) / 4
		}
		if choice.Message.Reasoning != nil {
			usage.OutputTokens += int64(len(choice.Message.Reasoning.Text)+3) / 4
		}
		for _, call := range choice.Message.ToolCalls {
			usage.OutputTokens += int64(len(call.Function.Name)+len(call.Function.Arguments)+3) / 4
		}
	}
	if response.Usage == nil {
		return usage
	}
	if response.Usage.InputTokens != nil {
		usage.InputTokens = *response.Usage.InputTokens
	}
	if response.Usage.OutputTokens != nil {
		usage.OutputTokens = *response.Usage.OutputTokens
	}
	usage.Source = response.Usage.Source
	return usage
}

func failureClass(kind canonical.ErrorKind) resilience.FailureClass {
	switch kind {
	case canonical.ErrorAuthentication:
		return resilience.FailureAuthentication
	case canonical.ErrorPermission:
		return resilience.FailurePermission
	case canonical.ErrorQuota:
		return resilience.FailureQuota
	case canonical.ErrorRateLimit:
		return resilience.FailureRateLimit
	case canonical.ErrorProviderTemporary:
		return resilience.FailureProviderTemporary
	case canonical.ErrorProviderPermanent, canonical.ErrorProviderConfiguration:
		return resilience.FailureProviderPermanent
	case canonical.ErrorStreamInterrupted:
		return resilience.FailureStreamInterrupted
	case canonical.ErrorStorageUnavailable:
		return resilience.FailureStorage
	case canonical.ErrorUncertain:
		return resilience.FailureUncertain
	default:
		return resilience.FailureInvalidRequest
	}
}

func retryAfter(value *canonical.RetryAfter, now time.Time) *resilience.RetryAfter {
	if value == nil {
		return nil
	}
	result := &resilience.RetryAfter{}
	if value.DelaySeconds != nil {
		result.Delay = time.Duration(*value.DelaySeconds) * time.Second
	}
	if value.At != nil {
		result.At = *value.At
	}
	_ = now
	return result
}

func retryAfterAt(value *canonical.RetryAfter, now time.Time) *time.Time {
	if value == nil {
		return nil
	}
	if value.At != nil {
		at := value.At.UTC()
		return &at
	}
	if value.DelaySeconds != nil {
		at := now.Add(time.Duration(*value.DelaySeconds) * time.Second)
		return &at
	}
	return nil
}

func waitUntil(ctx context.Context, at, now time.Time) error {
	delay := at.Sub(now)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func tripsCircuit(kind canonical.ErrorKind) bool {
	return kind == canonical.ErrorRateLimit || kind == canonical.ErrorProviderTemporary || kind == canonical.ErrorStreamInterrupted
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func asCanonical(err error, fallbackCode string) *canonical.Error {
	var providerError *canonical.Error
	if errors.As(err, &providerError) {
		return providerError
	}
	return &canonical.Error{Kind: canonical.ErrorProviderPermanent, Code: fallbackCode, Message: "provider returned an invalid response", Cause: err}
}

func storageError(code string, err error) *canonical.Error {
	return &canonical.Error{Kind: canonical.ErrorStorageUnavailable, Code: code, Message: "request state could not be persisted", Cause: err}
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
