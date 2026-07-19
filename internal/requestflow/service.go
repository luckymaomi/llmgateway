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
	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/resilience"
	"github.com/luckymaomi/llmgateway/internal/routing"
)

type Config struct {
	MaxResponseBytes int64
	Circuit          resilience.CircuitConfig
}

type Service struct {
	repository  Repository
	accounting  Accounting
	secrets     SecretResolver
	coordinator Coordinator
	factory     AdapterFactory
	router      *routing.Router
	retry       *resilience.RetryPolicy
	clock       Clock
	config      Config

	circuitMu sync.Mutex
	circuits  map[uuid.UUID]*resilience.Circuit
}

func New(repository Repository, accounting Accounting, secrets SecretResolver, coordinator Coordinator, factory AdapterFactory, router *routing.Router, retry *resilience.RetryPolicy, clock Clock, config Config) (*Service, error) {
	if repository == nil || accounting == nil || secrets == nil || coordinator == nil || factory == nil || router == nil || retry == nil || clock == nil {
		return nil, errors.New("request workflow dependencies are required")
	}
	if config.MaxResponseBytes < 1024 {
		return nil, errors.New("maximum provider response size must be at least 1024 bytes")
	}
	if config.Circuit.FailureThreshold < 1 || config.Circuit.SuccessThreshold < 1 || config.Circuit.OpenDuration <= 0 || config.Circuit.HalfOpenMaxInFlight < 1 {
		return nil, errors.New("request workflow circuit configuration is invalid")
	}
	return &Service{
		repository: repository, accounting: accounting, secrets: secrets, coordinator: coordinator,
		factory: factory, router: router, retry: retry, clock: clock, config: config,
		circuits: make(map[uuid.UUID]*resilience.Circuit),
	}, nil
}

func (s *Service) Models(ctx context.Context, userID uuid.UUID) ([]Model, error) {
	return s.repository.ListAuthorizedModels(ctx, userID)
}

func (s *Service) prepare(ctx context.Context, command ChatCommand) (execution, *canonical.Error) {
	model, err := s.repository.ResolveAuthorizedModel(ctx, command.Principal.UserID, command.Request.Model)
	if err != nil {
		return execution{}, workflowError(err)
	}
	if validationError := validateCapabilities(model, command.Request); validationError != nil {
		return execution{}, validationError
	}
	estimatedTokens := EstimateTokens(command.Request)
	if estimatedTokens > model.Capabilities.ContextTokens {
		return execution{}, &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "context_length_exceeded", Message: "request exceeds the configured model context", Parameter: "messages", HTTPStatus: http.StatusBadRequest}
	}
	revisionID, err := s.repository.ActiveConfigRevision(ctx)
	if err != nil {
		return execution{}, &canonical.Error{Kind: canonical.ErrorStorageUnavailable, Code: "configuration_unavailable", Message: "active configuration could not be read", Cause: err}
	}
	accepted, err := s.accounting.AcceptRequest(ctx, AcceptCommand{
		UserID: command.Principal.UserID, GatewayKeyID: command.Principal.KeyID, ModelID: model.ID,
		ResourceDomain: model.ResourceDomain, ConfigRevisionID: revisionID, IdempotencyKey: command.IdempotencyKey,
		RequestDigest: command.RequestDigest, Stream: command.Request.Stream, ReservedTokens: estimatedTokens,
	})
	if err != nil {
		return execution{}, workflowError(err)
	}
	if accepted.Existing {
		return execution{}, &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "request_already_accepted", Message: "idempotent request already exists", HTTPStatus: http.StatusConflict}
	}
	candidates, err := s.repository.ListCandidates(ctx, model.ID, model.ResourceDomain)
	if err != nil {
		_ = s.accounting.Release(ctx, accepted.RequestID, "storage_unavailable", "candidate lookup failed")
		return execution{}, &canonical.Error{Kind: canonical.ErrorStorageUnavailable, Code: "candidate_lookup_failed", Message: "upstream candidates could not be read", Cause: err}
	}
	if len(candidates) == 0 {
		_ = s.accounting.Release(ctx, accepted.RequestID, "upstream_unavailable", "no eligible credential")
		return execution{}, &canonical.Error{Kind: canonical.ErrorProviderTemporary, Code: domainUnavailableCode(model), Message: "no eligible upstream credential is available"}
	}
	upstreamRequest := command.Request
	upstreamRequest.Model = model.UpstreamName
	return execution{command: command, model: model, request: upstreamRequest, accepted: accepted, candidates: candidates, estimatedTokens: estimatedTokens}, nil
}

type execution struct {
	command         ChatCommand
	model           Model
	request         canonical.ChatRequest
	accepted        Accepted
	candidates      []Candidate
	estimatedTokens int64
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

func (s *Service) candidateDecision(run execution, excluded []routing.CandidateID) (Candidate, *resilience.Permit, *canonical.Error) {
	required := requiredCapabilities(run.request)
	routeCandidates := make([]routing.Candidate, 0, len(run.candidates))
	byID := make(map[routing.CandidateID]Candidate, len(run.candidates))
	now := s.clock.Now().UTC()
	for _, candidate := range run.candidates {
		id := routing.CandidateID(candidate.ID.String())
		byID[id] = candidate
		success := int32(1000 - min(int(candidate.ConsecutiveFailures)*100, 900))
		routeCandidates = append(routeCandidates, routing.Candidate{
			ID: id, ModelID: routing.ModelID(run.model.ID.String()), ResourceDomain: routing.ResourceDomain(run.model.ResourceDomain),
			ModelPublished: true, CredentialAuthorized: true, CredentialActive: true,
			Capabilities: required, CooldownUntil: timeOrZero(candidate.CooldownUntil),
			ExitHealthy: true, Quota: routing.Quota{Source: routing.SourceUnknown}, AdminPriority: candidate.Priority,
			SuccessPermille: success, ErrorPermille: 1000 - success,
		})
	}
	decision, err := s.router.Select(routing.Requirements{
		ModelID: routing.ModelID(run.model.ID.String()), ResourceDomain: routing.ResourceDomain(run.model.ResourceDomain),
		Capabilities: required, EstimatedTokens: run.estimatedTokens, ExcludedCandidates: excluded, At: now,
	}, routeCandidates)
	if err != nil {
		return Candidate{}, nil, &canonical.Error{Kind: canonical.ErrorInternalInvariant, Code: "routing_failed", Message: "upstream routing failed", Cause: err}
	}
	if decision.SelectedCandidateID == "" {
		return Candidate{}, nil, &canonical.Error{Kind: canonical.ErrorProviderTemporary, Code: domainUnavailableCode(run.model), Message: "no eligible upstream credential is available"}
	}
	candidate := byID[decision.SelectedCandidateID]
	circuit, err := s.circuit(candidate.ID)
	if err != nil {
		return Candidate{}, nil, &canonical.Error{Kind: canonical.ErrorInternalInvariant, Code: "circuit_failed", Message: "upstream circuit could not be initialized", Cause: err}
	}
	acquired := circuit.Acquire()
	if !acquired.Allowed {
		return Candidate{}, nil, &canonical.Error{Kind: canonical.ErrorProviderTemporary, Code: "upstream_circuit_open", Message: "upstream credential is cooling down", RetryAfter: &canonical.RetryAfter{At: &acquired.RetryAt}}
	}
	return candidate, acquired.Permit, nil
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
