package requestflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/canonical"
	"github.com/luckymaomi/llmgateway/internal/execution"
	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/resilience"
	"github.com/luckymaomi/llmgateway/internal/routing"
)

type Config struct {
	MaxResponseBytes           int64
	ExecutionHeartbeatInterval time.Duration
	Circuit                    resilience.CircuitConfig
}

type Service struct {
	repository  Repository
	accounting  Accounting
	secrets     SecretResolver
	admitter    Admitter
	coordinator Coordinator
	factory     AdapterFactory
	router      *routing.Router
	retry       *resilience.RetryPolicy
	clock       Clock
	config      Config
	observer    Observer

	circuitMu sync.Mutex
	circuits  map[uuid.UUID]*resilience.Circuit
}

func New(repository Repository, accounting Accounting, secrets SecretResolver, admitter Admitter, coordinator Coordinator, factory AdapterFactory, router *routing.Router, retry *resilience.RetryPolicy, clock Clock, config Config) (*Service, error) {
	if repository == nil || accounting == nil || secrets == nil || admitter == nil || coordinator == nil || factory == nil || router == nil || retry == nil || clock == nil {
		return nil, errors.New("request workflow dependencies are required")
	}
	if config.MaxResponseBytes < 1024 {
		return nil, errors.New("maximum provider response size must be at least 1024 bytes")
	}
	if config.ExecutionHeartbeatInterval <= 0 {
		return nil, errors.New("execution heartbeat interval must be positive")
	}
	if config.Circuit.FailureThreshold < 1 || config.Circuit.SuccessThreshold < 1 || config.Circuit.OpenDuration <= 0 || config.Circuit.HalfOpenMaxInFlight < 1 {
		return nil, errors.New("request workflow circuit configuration is invalid")
	}
	return &Service{
		repository: repository, accounting: accounting, secrets: secrets, admitter: admitter, coordinator: coordinator,
		factory: factory, router: router, retry: retry, clock: clock, config: config,
		observer: noopObserver{}, circuits: make(map[uuid.UUID]*resilience.Circuit),
	}, nil
}

func (s *Service) WithObserver(observer Observer) *Service {
	if observer != nil {
		s.observer = observer
	}
	return s
}

type noopObserver struct{}

func (noopObserver) ProviderAttempt(providers.Kind, string, string) {}

func (s *Service) observeAttempt(kind providers.Kind, err *canonical.Error) {
	if err == nil {
		s.observer.ProviderAttempt(kind, "succeeded", "none")
		return
	}
	outcome := "failed"
	if err.Kind == canonical.ErrorUncertain || err.Kind == canonical.ErrorStreamInterrupted {
		outcome = "uncertain"
	}
	s.observer.ProviderAttempt(kind, outcome, string(err.Kind))
}

func (s *Service) Models(ctx context.Context, gatewayKeyID uuid.UUID) ([]Model, error) {
	return s.repository.ListPublishedModels(ctx, gatewayKeyID)
}

func (s *Service) prepare(ctx context.Context, command ChatCommand) (workflowRun, *canonical.Error) {
	model, err := s.repository.ResolvePublishedModel(ctx, command.Principal.KeyID, command.Request.Model)
	if err != nil {
		return workflowRun{}, workflowError(err)
	}
	if validationError := validateCapabilities(model, command.Request); validationError != nil {
		return workflowRun{}, validationError
	}
	estimatedTokens := EstimateTokens(command.Request)
	if estimatedTokens > model.Capabilities.ContextTokens {
		return workflowRun{}, &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "context_length_exceeded", Message: "request exceeds the configured model context", Parameter: "messages", HTTPStatus: http.StatusBadRequest}
	}
	candidates, err := s.repository.ListPublishedCandidates(ctx, model.ConfigRevisionID, model.ID, model.ResourceDomain)
	if err != nil {
		return workflowRun{}, &canonical.Error{Kind: canonical.ErrorStorageUnavailable, Code: "candidate_lookup_failed", Message: "upstream candidates could not be read", Cause: err}
	}
	if len(candidates) == 0 {
		return workflowRun{}, &canonical.Error{Kind: canonical.ErrorProviderTemporary, Code: domainUnavailableCode(model), Message: "no eligible upstream credential is available"}
	}
	upstreamRequest := command.Request
	upstreamRequest.Model = model.UpstreamName
	run := workflowRun{command: command, model: model, request: upstreamRequest, candidates: candidates, estimatedTokens: estimatedTokens}
	candidate, selectionError := s.selectCandidate(run, nil)
	if selectionError != nil {
		return workflowRun{}, selectionError
	}
	requestID := command.RequestID
	if requestID == uuid.Nil {
		requestID = uuid.New()
	}
	admissionPermit, _, err := s.admitter.Acquire(ctx, AdmissionRequest{RequestID: requestID, UserID: command.Principal.UserID})
	if err != nil {
		return workflowRun{}, admissionError(err)
	}
	releaseAdmission := func() {
		if admissionPermit != nil {
			admissionPermit.Release()
			admissionPermit = nil
		}
	}
	revisionID := model.ConfigRevisionID
	accepted, err := s.accounting.AcceptRequest(ctx, AcceptCommand{
		RequestID: requestID, UserID: command.Principal.UserID, GatewayKeyID: command.Principal.KeyID, ModelID: model.ID,
		ResourceDomain: model.ResourceDomain, ConfigRevisionID: &revisionID, IdempotencyKey: command.IdempotencyKey,
		RequestDigest: command.RequestDigest, Stream: command.Request.Stream, ReservedTokens: estimatedTokens,
	})
	if err != nil {
		releaseAdmission()
		return workflowRun{}, workflowError(err)
	}
	if accepted.Existing {
		releaseAdmission()
		return workflowRun{}, &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "request_already_accepted", Message: "idempotent request already exists", HTTPStatus: http.StatusConflict}
	}
	if accepted.RequestID != requestID || accepted.ReservationID == uuid.Nil || accepted.EntitlementID == uuid.Nil || accepted.EntitlementConcurrency < 1 {
		releaseAdmission()
		if accepted.RequestID != uuid.Nil {
			_ = s.accounting.ReleaseAccepted(context.WithoutCancel(ctx), accepted.RequestID, "invalid_acceptance", "accepted request is missing its coordination capacity")
		}
		return workflowRun{}, &canonical.Error{Kind: canonical.ErrorInternalInvariant, Code: "invalid_acceptance", Message: "accepted request is missing required execution capacity"}
	}
	if command.AcceptedSink != nil {
		if err := command.AcceptedSink(context.WithoutCancel(ctx), accepted.RequestID); err != nil {
			releaseAdmission()
			_ = s.accounting.ReleaseAccepted(context.WithoutCancel(ctx), accepted.RequestID, "acceptance_persistence_failed", "accepted request could not be linked to its caller")
			return workflowRun{}, storageError("acceptance_persistence_failed", err)
		}
	}
	claim, err := s.repository.ClaimExecution(ctx, accepted.RequestID, uuid.New())
	if err != nil {
		releaseAdmission()
		_ = s.accounting.ReleaseAccepted(context.WithoutCancel(ctx), accepted.RequestID, "execution_claim_failed", "request execution could not be claimed")
		return workflowRun{}, storageError("execution_claim_failed", err)
	}
	run.accepted = accepted
	run.claim = claim
	run.context, run.stopHeartbeat = s.executionContext(ctx, claim)
	lease, _, err := s.coordinator.Acquire(run.context, s.leaseRequest(claim, run, candidate))
	if err != nil {
		run.stopExecution()
		releaseAdmission()
		capacityError := admissionError(err)
		_ = s.accounting.Release(context.WithoutCancel(ctx), claim, capacityError.Code, capacityError.Message)
		return workflowRun{}, capacityError
	}
	run.admissionPermit = admissionPermit
	run.initialLease = lease
	run.initialCandidate = candidate
	return run, nil
}

type workflowRun struct {
	command          ChatCommand
	model            Model
	request          canonical.ChatRequest
	accepted         Accepted
	claim            execution.Claim
	context          context.Context
	stopHeartbeat    context.CancelFunc
	admissionPermit  AdmissionPermit
	candidates       []Candidate
	estimatedTokens  int64
	initialLease     Lease
	initialCandidate Candidate
}

func (run *workflowRun) releaseAdmission() {
	if run == nil || run.admissionPermit == nil {
		return
	}
	run.admissionPermit.Release()
	run.admissionPermit = nil
}

func (run *workflowRun) stopExecution() {
	if run == nil || run.stopHeartbeat == nil {
		return
	}
	run.stopHeartbeat()
	run.stopHeartbeat = nil
}

func validateCapabilities(model Model, request canonical.ChatRequest) *canonical.Error {
	unsupported := func(parameter string) *canonical.Error {
		return &canonical.Error{Kind: canonical.ErrorUnsupportedCapability, Code: "unsupported_capability", Message: "model does not support the requested capability", Parameter: parameter, HTTPStatus: http.StatusBadRequest}
	}
	if request.Stream && !model.Capabilities.Streaming {
		return unsupported("stream")
	}
	if len(request.Tools) > 0 && !model.Capabilities.Tools {
		return unsupported("tools")
	}
	if request.Reasoning != nil && !model.Capabilities.Reasoning {
		return unsupported("thinking")
	}
	if request.ResponseFormat != nil && request.ResponseFormat.Type != canonical.ResponseFormatText && !model.Capabilities.StructuredOutput {
		return unsupported("response_format")
	}
	if request.MaxOutputTokens != nil && *request.MaxOutputTokens > model.Capabilities.OutputTokens {
		return &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "max_output_tokens_exceeded", Message: "requested output exceeds the model limit", Parameter: "max_completion_tokens", HTTPStatus: http.StatusBadRequest}
	}
	return nil
}

func (s *Service) candidateDecision(run workflowRun, excluded []routing.CandidateID) (Candidate, *resilience.Permit, *canonical.Error) {
	selectionExcluded := append([]routing.CandidateID(nil), excluded...)
	var earliestRetryAt *time.Time
	circuitUnavailable := false
	for {
		candidate, selectionError := s.selectCandidate(run, selectionExcluded)
		if selectionError != nil {
			if !circuitUnavailable {
				return Candidate{}, nil, selectionError
			}
			earliestRetryAt = earlierRetryAt(earliestRetryAt, retryAfterAt(selectionError.RetryAfter, s.clock.Now().UTC()))
			return Candidate{}, nil, circuitUnavailableError(earliestRetryAt)
		}
		permit, circuitError := s.acquireCircuit(candidate.ID)
		if circuitError == nil {
			return candidate, permit, nil
		}
		if circuitError.Code != "upstream_circuit_open" {
			return Candidate{}, nil, circuitError
		}
		circuitUnavailable = true
		earliestRetryAt = earlierRetryAt(earliestRetryAt, retryAfterAt(circuitError.RetryAfter, s.clock.Now().UTC()))
		selectionExcluded = append(selectionExcluded, routing.CandidateID(candidate.ID.String()))
	}
}

func (s *Service) attemptDecision(ctx context.Context, run *workflowRun, excluded []routing.CandidateID, attemptNumber int) (Candidate, *resilience.Permit, Lease, *canonical.Error) {
	if attemptNumber != 1 {
		candidate, permit, selectionError := s.candidateDecision(*run, excluded)
		return candidate, permit, nil, selectionError
	}

	candidate := run.initialCandidate
	lease := run.initialLease
	run.initialLease = nil
	permit, circuitError := s.acquireCircuit(candidate.ID)
	if circuitError == nil {
		return candidate, permit, lease, nil
	}
	if lease != nil {
		_ = lease.Release(context.WithoutCancel(ctx))
	}
	if circuitError.Code != "upstream_circuit_open" {
		return Candidate{}, nil, nil, circuitError
	}

	selectionExcluded := append([]routing.CandidateID(nil), excluded...)
	selectionExcluded = append(selectionExcluded, routing.CandidateID(candidate.ID.String()))
	nextCandidate, nextPermit, selectionError := s.candidateDecision(*run, selectionExcluded)
	if selectionError == nil {
		return nextCandidate, nextPermit, nil, nil
	}
	retryAt := earlierRetryAt(
		retryAfterAt(circuitError.RetryAfter, s.clock.Now().UTC()),
		retryAfterAt(selectionError.RetryAfter, s.clock.Now().UTC()),
	)
	return Candidate{}, nil, nil, circuitUnavailableError(retryAt)
}

func circuitUnavailableError(retryAt *time.Time) *canonical.Error {
	providerError := &canonical.Error{
		Kind: canonical.ErrorProviderTemporary, Code: "upstream_circuit_open", Message: "all eligible upstream credentials are cooling down",
	}
	if retryAt != nil && !retryAt.IsZero() {
		at := retryAt.UTC()
		providerError.RetryAfter = &canonical.RetryAfter{At: &at}
	}
	return providerError
}

func earlierRetryAt(left, right *time.Time) *time.Time {
	if left == nil || left.IsZero() {
		return right
	}
	if right == nil || right.IsZero() || left.Before(*right) {
		return left
	}
	return right
}

func (s *Service) selectCandidate(run workflowRun, excluded []routing.CandidateID) (Candidate, *canonical.Error) {
	required := requiredCapabilities(run.request)
	routeCandidates := make([]routing.Candidate, 0, len(run.candidates))
	byID := make(map[routing.CandidateID]Candidate, len(run.candidates))
	now := s.clock.Now().UTC()
	for _, candidate := range run.candidates {
		id := routing.CandidateID(candidate.ID.String())
		byID[id] = candidate
		routeCandidates = append(routeCandidates, routing.Candidate{
			ID: id, ModelID: routing.ModelID(run.model.ID.String()), ResourceDomain: routing.ResourceDomain(run.model.ResourceDomain),
			ModelPublished: true, CredentialAuthorized: true, CredentialActive: true,
			Capabilities: required, CooldownUntil: timeOrZero(candidate.CooldownUntil),
			AdminPriority: candidate.Priority, Weight: candidate.Weight,
		})
	}
	decision, err := s.router.Select(routing.Requirements{
		ModelID: routing.ModelID(run.model.ID.String()), ResourceDomain: routing.ResourceDomain(run.model.ResourceDomain),
		Capabilities: required, ExcludedCandidates: excluded, At: now,
	}, routeCandidates)
	if err != nil {
		return Candidate{}, &canonical.Error{Kind: canonical.ErrorInternalInvariant, Code: "routing_failed", Message: "upstream routing failed", Cause: err}
	}
	if decision.SelectedCandidateID == "" {
		providerError := &canonical.Error{Kind: canonical.ErrorProviderTemporary, Code: domainUnavailableCode(run.model), Message: "no eligible upstream credential is available"}
		if !decision.NextAvailableAt.IsZero() {
			retryAt := decision.NextAvailableAt.UTC()
			providerError.RetryAfter = &canonical.RetryAfter{At: &retryAt}
		}
		return Candidate{}, providerError
	}
	candidate := byID[decision.SelectedCandidateID]
	return candidate, nil
}

func (s *Service) acquireCircuit(candidateID uuid.UUID) (*resilience.Permit, *canonical.Error) {
	circuit, err := s.circuit(candidateID)
	if err != nil {
		return nil, &canonical.Error{Kind: canonical.ErrorInternalInvariant, Code: "circuit_failed", Message: "upstream circuit could not be initialized", Cause: err}
	}
	acquired := circuit.Acquire()
	if !acquired.Allowed {
		providerError := &canonical.Error{Kind: canonical.ErrorProviderTemporary, Code: "upstream_circuit_open", Message: "upstream credential is cooling down"}
		if !acquired.RetryAt.IsZero() {
			retryAt := acquired.RetryAt.UTC()
			providerError.RetryAfter = &canonical.RetryAfter{At: &retryAt}
		}
		return nil, providerError
	}
	return acquired.Permit, nil
}

func (s *Service) leaseRequest(claim execution.Claim, run workflowRun, candidate Candidate) LeaseRequest {
	return LeaseRequest{RequestID: claim.RequestID, ExecutionID: claim.ExecutionID, UserID: run.command.Principal.UserID, GatewayKeyID: run.command.Principal.KeyID,
		ModelID: run.model.ID, ProviderID: run.model.ProviderID, CredentialID: candidate.ID,
		EntitlementID: run.accepted.EntitlementID, ResourceDomain: run.model.ResourceDomain,
		EstimatedTokens: run.estimatedTokens,
		RPMLimit:        candidate.RPMLimit, TPMLimit: candidate.TPMLimit, Concurrency: candidate.ConcurrencyLimit,
		EntitlementConcurrency: run.accepted.EntitlementConcurrency,
		EntitlementRPMLimit:    run.accepted.EntitlementRPMLimit,
		EntitlementTPMLimit:    run.accepted.EntitlementTPMLimit}
}

func admissionError(err error) *canonical.Error {
	var capacity *CapacityError
	if errors.As(err, &capacity) {
		return &canonical.Error{Kind: canonical.ErrorRateLimit, Code: "admission_capacity_exhausted", Message: "request capacity is exhausted; retry after the reported time", RetryAfter: &canonical.RetryAfter{At: &capacity.RetryAt}, Cause: err}
	}
	switch {
	case errors.Is(err, ErrAdmissionQueueFull):
		return &canonical.Error{Kind: canonical.ErrorRateLimit, Code: "admission_queue_full", Message: "the request queue is full", HTTPStatus: http.StatusTooManyRequests, Cause: err}
	case errors.Is(err, ErrAdmissionTimedOut):
		return &canonical.Error{Kind: canonical.ErrorRateLimit, Code: "admission_timeout", Message: "the request waited too long for capacity", HTTPStatus: http.StatusTooManyRequests, Cause: err}
	case errors.Is(err, ErrAdmissionCanceled):
		return &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "request_canceled", Message: "the request was canceled while waiting for capacity", HTTPStatus: http.StatusRequestTimeout, Cause: err}
	case errors.Is(err, ErrCoordinationFailed):
		return &canonical.Error{Kind: canonical.ErrorStorageUnavailable, Code: "admission_unavailable", Message: "request coordination is temporarily unavailable", HTTPStatus: http.StatusServiceUnavailable, Cause: err}
	}
	return &canonical.Error{Kind: canonical.ErrorInternalInvariant, Code: "admission_failed", Message: "request admission failed", Cause: err}
}

func (s *Service) circuit(candidateID uuid.UUID) (*resilience.Circuit, error) {
	s.circuitMu.Lock()
	defer s.circuitMu.Unlock()
	if circuit := s.circuits[candidateID]; circuit != nil {
		return circuit, nil
	}
	circuit, err := resilience.NewCircuit(s.config.Circuit, s.clock)
	if err != nil {
		return nil, err
	}
	s.circuits[candidateID] = circuit
	return circuit, nil
}

func requiredCapabilities(request canonical.ChatRequest) []routing.Capability {
	capabilities := []routing.Capability{"chat"}
	if request.Stream {
		capabilities = append(capabilities, "streaming")
	}
	if len(request.Tools) > 0 {
		capabilities = append(capabilities, "tools")
	}
	if request.Reasoning != nil {
		capabilities = append(capabilities, "reasoning")
	}
	if request.ResponseFormat != nil && request.ResponseFormat.Type != canonical.ResponseFormatText {
		capabilities = append(capabilities, "structured_output")
	}
	return capabilities
}

func timeOrZero(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

func domainUnavailableCode(model Model) string {
	if model.ResourceDomain == "free" {
		return "free_pool_unavailable"
	}
	return "professional_pool_unavailable"
}

func workflowError(err error) *canonical.Error {
	switch {
	case errors.Is(err, ErrModelNotFound):
		return &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "model_not_found", Message: "model was not found", Parameter: "model", HTTPStatus: http.StatusNotFound}
	case errors.Is(err, ErrModelNotAuthorized):
		return &canonical.Error{Kind: canonical.ErrorPermission, Code: "model_not_authorized", Message: "model is not authorized for this key", Parameter: "model", HTTPStatus: http.StatusForbidden}
	case errors.Is(err, ErrIdempotencyConflict):
		return &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "idempotency_conflict", Message: "idempotency key was reused with a different request", HTTPStatus: http.StatusConflict}
	case errors.Is(err, ErrQuotaExhausted):
		return &canonical.Error{Kind: canonical.ErrorQuota, Code: "quota_exhausted", Message: "no applicable entitlement has enough remaining tokens", HTTPStatus: http.StatusPaymentRequired}
	case errors.Is(err, ErrCostConfigurationMissing):
		return &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "cost_configuration_missing", Message: "the model has no effective cost configuration", Parameter: "model", HTTPStatus: http.StatusConflict}
	case errors.Is(err, ErrInvalidAccounting):
		return &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "invalid_accounting_request", Message: "request cannot be reserved", HTTPStatus: http.StatusBadRequest}
	default:
		return &canonical.Error{Kind: canonical.ErrorStorageUnavailable, Code: "request_state_unavailable", Message: "request state could not be persisted", Cause: err}
	}
}

func readResponse(response *http.Response, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("provider response exceeds %d bytes", limit)
	}
	return body, nil
}

func providerCredential(secret string) providers.Credential {
	return providers.Credential{APIKey: secret}
}
