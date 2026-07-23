package registry

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/security"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$`)

const credentialProbePersistenceTimeout = 3 * time.Second

type Service struct {
	repository Repository
	envelope   *security.EnvelopeCipher
	prober     CredentialProbeExecutor
	catalog    *providers.Catalog
}

func NewService(repository Repository, envelope *security.EnvelopeCipher, urls *security.URLValidator) (*Service, error) {
	if repository == nil || envelope == nil || urls == nil {
		return nil, fmt.Errorf("registry dependencies are required")
	}
	return &Service{repository: repository, envelope: envelope, catalog: providers.DefaultCatalog()}, nil
}

func (s *Service) WithCredentialProbeExecutor(prober CredentialProbeExecutor) *Service {
	s.prober = prober
	return s
}

func (s *Service) SyncCatalog(ctx context.Context) error {
	presets := s.catalog.Presets()
	projections := make([]ProviderProjection, 0, len(presets))
	for _, preset := range presets {
		verifiedAt, err := time.Parse(time.DateOnly, preset.VerifiedAt)
		if err != nil {
			return fmt.Errorf("catalog preset %s verified date: %w", preset.ID, err)
		}
		projection := ProviderProjection{
			CatalogID: preset.ID, Slug: preset.Slug, Name: preset.Name, Kind: preset.Kind,
			BaseURL: preset.BaseURL, SourceURL: preset.SourceURL, VerifiedAt: verifiedAt.UTC(),
			Models: make([]ModelProjection, 0, len(preset.Models)),
		}
		for _, model := range preset.Models {
			projection.Models = append(projection.Models, ModelProjection{
				PublicName: model.PublicName, UpstreamName: model.UpstreamName, DisplayName: model.DisplayName,
				Capabilities: modelCapabilities(model),
			})
		}
		projections = append(projections, projection)
	}
	return s.repository.SyncCatalog(ctx, projections)
}

func modelCapabilities(model providers.ModelPreset) ModelCapabilities {
	capabilities := ModelCapabilities{Chat: true, ContextTokens: model.ContextTokens, OutputTokens: min(model.ContextTokens, 8192), ReasoningMode: ReasoningMode(model.ReasoningMode)}
	for _, capability := range model.Capabilities {
		switch capability {
		case "streaming":
			capabilities.Streaming = true
		case "tools":
			capabilities.Tools = true
		case "reasoning":
			capabilities.Reasoning = true
		case "structured_output":
			capabilities.StructuredOutput = true
		}
	}
	return capabilities
}

func (s *Service) ListProviders(ctx context.Context, actor identity.Principal) ([]Provider, error) {
	if !activeAdministrator(actor) {
		return nil, ErrForbidden
	}
	items, err := s.repository.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	for index := range items {
		s.enrichProvider(&items[index])
	}
	return items, nil
}

func (s *Service) GetProvider(ctx context.Context, actor identity.Principal, providerID uuid.UUID) (Provider, error) {
	if !activeAdministrator(actor) || providerID == uuid.Nil {
		return Provider{}, ErrForbidden
	}
	provider, err := s.repository.GetProvider(ctx, providerID)
	if err == nil {
		s.enrichProvider(&provider)
	}
	return provider, err
}

func (s *Service) provider(ctx context.Context, providerID uuid.UUID) (Provider, error) {
	provider, err := s.repository.GetProvider(ctx, providerID)
	if err == nil {
		s.enrichProvider(&provider)
	}
	return provider, err
}

func (s *Service) enrichProvider(provider *Provider) {
	for _, info := range s.catalog.Kinds() {
		if info.Kind == provider.Kind {
			provider.Contract = info.Contract
			return
		}
	}
}

func (s *Service) ListModels(ctx context.Context, actor identity.Principal) ([]Model, error) {
	if actor.Status != identity.StatusActive {
		return nil, ErrForbidden
	}
	return s.repository.ListModels(ctx)
}

func (s *Service) CreateResourcePool(ctx context.Context, actor identity.Principal, input NewResourcePool, request MutationRequest) (ResourcePool, error) {
	if !activeAdministrator(actor) {
		return ResourcePool{}, ErrForbidden
	}
	input.Slug, input.Name = strings.TrimSpace(input.Slug), strings.TrimSpace(input.Name)
	if input.ProviderID == uuid.Nil || !slugPattern.MatchString(input.Slug) || utf8.RuneCountInString(input.Name) < 2 || utf8.RuneCountInString(input.Name) > 80 || !validModelIDs(input.ModelIDs) {
		return ResourcePool{}, ErrInvalidInput
	}
	models, err := s.repository.ListModels(ctx)
	if err != nil {
		return ResourcePool{}, err
	}
	available := make(map[uuid.UUID]uuid.UUID, len(models))
	for _, model := range models {
		available[model.ID] = model.ProviderID
	}
	for _, modelID := range input.ModelIDs {
		if available[modelID] != input.ProviderID {
			return ResourcePool{}, ErrInvalidInput
		}
	}
	mutation, err := resourcePoolMutation(request, "resource_pool.create", input)
	if err != nil {
		return ResourcePool{}, err
	}
	return s.repository.CreateResourcePool(ctx, input, actor.UserID, mutation)
}

func (s *Service) UpdateResourcePool(ctx context.Context, actor identity.Principal, input ResourcePoolChange, request MutationRequest) (ResourcePool, error) {
	if !activeAdministrator(actor) {
		return ResourcePool{}, ErrForbidden
	}
	input.Name = strings.TrimSpace(input.Name)
	input.ExpectedUpdatedAt = input.ExpectedUpdatedAt.UTC()
	if input.ID == uuid.Nil || input.ExpectedUpdatedAt.IsZero() || utf8.RuneCountInString(input.Name) < 2 || utf8.RuneCountInString(input.Name) > 80 {
		return ResourcePool{}, ErrInvalidInput
	}
	mutation, err := resourcePoolMutation(request, "resource_pool.update", input)
	if err != nil {
		return ResourcePool{}, err
	}
	return s.repository.UpdateResourcePool(ctx, input, actor.UserID, mutation)
}

func (s *Service) SetResourcePoolStatus(ctx context.Context, actor identity.Principal, id uuid.UUID, status ResourcePoolStatus, expectedUpdatedAt time.Time, request MutationRequest) (ResourcePool, error) {
	if !activeAdministrator(actor) {
		return ResourcePool{}, ErrForbidden
	}
	if id == uuid.Nil || expectedUpdatedAt.IsZero() || status != ResourcePoolActive && status != ResourcePoolDisabled && status != ResourcePoolRetired {
		return ResourcePool{}, ErrInvalidInput
	}
	payload := struct {
		ID                uuid.UUID          `json:"id"`
		Status            ResourcePoolStatus `json:"status"`
		ExpectedUpdatedAt time.Time          `json:"expected_updated_at"`
	}{id, status, expectedUpdatedAt.UTC().Truncate(time.Microsecond)}
	mutation, err := resourcePoolMutation(request, "resource_pool.status", payload)
	if err != nil {
		return ResourcePool{}, err
	}
	return s.repository.SetResourcePoolStatus(ctx, id, status, expectedUpdatedAt.UTC(), actor.UserID, mutation)
}

func (s *Service) ListResourcePools(ctx context.Context, actor identity.Principal, includeRetired bool) ([]ResourcePool, error) {
	if !activeAdministrator(actor) {
		return nil, ErrForbidden
	}
	return s.repository.ListResourcePools(ctx, includeRetired)
}

func (s *Service) GetResourcePool(ctx context.Context, actor identity.Principal, id uuid.UUID) (ResourcePool, error) {
	if !activeAdministrator(actor) || id == uuid.Nil {
		return ResourcePool{}, ErrForbidden
	}
	return s.repository.GetResourcePool(ctx, id)
}

func (s *Service) CreateCredential(ctx context.Context, actor identity.Principal, input NewCredential, secret string, request MutationRequest) (Credential, error) {
	if !activeAdministrator(actor) {
		return Credential{}, ErrForbidden
	}
	input.ID = uuid.New()
	input.Name = strings.TrimSpace(input.Name)
	if input.ResourcePoolID == uuid.Nil || len(secret) < 8 || len(secret) > 8192 || !validCredentialFields(input.Name, input.RPMLimit, input.TPMLimit, input.ConcurrencyLimit, input.ModelBindings) {
		return Credential{}, ErrInvalidInput
	}
	mutation, err := credentialCreateMutation(request, input, secret)
	if err != nil {
		return Credential{}, err
	}
	input.EncryptedSecret, err = s.envelope.Encrypt([]byte(secret), CredentialEncryptionContext(input.ID))
	if err != nil {
		return Credential{}, fmt.Errorf("encrypt credential: %w", err)
	}
	return s.repository.CreateCredential(ctx, input, actor.UserID, mutation)
}

func (s *Service) ImportCredentials(ctx context.Context, actor identity.Principal, resourcePoolID uuid.UUID, items []CredentialBatchItem, bindings []CredentialModelBinding, rpmLimit *int32, tpmLimit *int64, concurrencyLimit *int32, request MutationRequest) ([]CredentialBatchResult, error) {
	if !activeAdministrator(actor) || resourcePoolID == uuid.Nil || len(items) == 0 || len(items) > 500 {
		return nil, ErrForbidden
	}
	results := make([]CredentialBatchResult, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for index, item := range items {
		item.Name, item.Secret = strings.TrimSpace(item.Name), strings.TrimSpace(item.Secret)
		result := CredentialBatchResult{Line: index + 1, Name: item.Name}
		if _, duplicate := seen[item.Secret]; duplicate {
			result.Status = "skipped"
			results = append(results, result)
			continue
		}
		seen[item.Secret] = struct{}{}
		childRequest := request
		childRequest.IdempotencyKey = uuid.NewSHA1(request.IdempotencyKey, []byte(fmt.Sprintf("credential-line:%d", index+1)))
		created, err := s.CreateCredential(ctx, actor, NewCredential{ResourcePoolID: resourcePoolID, Name: item.Name, RPMLimit: rpmLimit, TPMLimit: tpmLimit, ConcurrencyLimit: concurrencyLimit, ModelBindings: bindings}, item.Secret, childRequest)
		if err != nil {
			result.Status, result.ErrorKind = "rejected", credentialImportError(err)
		} else {
			result.Status, result.Credential = "created", &created
		}
		results = append(results, result)
	}
	return results, nil
}

func credentialImportError(err error) string {
	switch {
	case errors.Is(err, ErrInvalidInput):
		return "invalid_input"
	case errors.Is(err, ErrConflict), errors.Is(err, ErrIdempotencyConflict):
		return "conflict"
	default:
		return "persistence_failed"
	}
}

func (s *Service) UpdateCredential(ctx context.Context, actor identity.Principal, input CredentialChange, secret string, request MutationRequest) (Credential, error) {
	if !activeAdministrator(actor) {
		return Credential{}, ErrForbidden
	}
	input.Name, input.ReplaceSecret, input.ExpectedUpdatedAt = strings.TrimSpace(input.Name), secret != "", input.ExpectedUpdatedAt.UTC()
	if input.ID == uuid.Nil || input.ExpectedUpdatedAt.IsZero() || !validCredentialFields(input.Name, input.RPMLimit, input.TPMLimit, input.ConcurrencyLimit, input.ModelBindings) || input.ReplaceSecret && (len(secret) < 8 || len(secret) > 8192) {
		return Credential{}, ErrInvalidInput
	}
	mutation, err := credentialUpdateMutation(request, input, secret)
	if err != nil {
		return Credential{}, err
	}
	if input.ReplaceSecret {
		input.EncryptedSecret, err = s.envelope.Encrypt([]byte(secret), CredentialEncryptionContext(input.ID))
		if err != nil {
			return Credential{}, fmt.Errorf("encrypt credential: %w", err)
		}
	}
	return s.repository.UpdateCredential(ctx, input, actor.UserID, mutation)
}

func (s *Service) SetCredentialStatus(ctx context.Context, actor identity.Principal, id uuid.UUID, status CredentialStatus, expectedUpdatedAt time.Time, request MutationRequest) (Credential, error) {
	if !activeAdministrator(actor) {
		return Credential{}, ErrForbidden
	}
	if id == uuid.Nil || expectedUpdatedAt.IsZero() || status != CredentialActive && status != CredentialDisabled {
		return Credential{}, ErrInvalidInput
	}
	mutation, err := mutationFingerprint(request, "credential.status", struct {
		ID                uuid.UUID        `json:"id"`
		Status            CredentialStatus `json:"status"`
		ExpectedUpdatedAt time.Time        `json:"expected_updated_at"`
	}{id, status, expectedUpdatedAt.UTC().Truncate(time.Microsecond)})
	if err != nil {
		return Credential{}, err
	}
	return s.repository.SetCredentialStatus(ctx, id, status, expectedUpdatedAt.UTC(), actor.UserID, mutation)
}

func (s *Service) RetireCredential(ctx context.Context, actor identity.Principal, id uuid.UUID, expectedUpdatedAt time.Time, request MutationRequest) (Credential, error) {
	if !activeAdministrator(actor) || id == uuid.Nil || expectedUpdatedAt.IsZero() {
		return Credential{}, ErrForbidden
	}
	mutation, err := mutationFingerprint(request, "credential.retire", struct {
		ID                uuid.UUID `json:"id"`
		ExpectedUpdatedAt time.Time `json:"expected_updated_at"`
	}{id, expectedUpdatedAt.UTC().Truncate(time.Microsecond)})
	if err != nil {
		return Credential{}, err
	}
	tombstone, err := s.envelope.Encrypt([]byte("retired"), CredentialEncryptionContext(id))
	if err != nil {
		return Credential{}, err
	}
	return s.repository.RetireCredential(ctx, id, tombstone, expectedUpdatedAt.UTC(), actor.UserID, mutation)
}

func (s *Service) ProbeCredential(ctx context.Context, actor identity.Principal, credentialID, modelID uuid.UUID, requestID string) (CredentialProbeExecution, Credential, error) {
	if !activeAdministrator(actor) || credentialID == uuid.Nil || modelID == uuid.Nil || strings.TrimSpace(requestID) == "" || len(requestID) > 128 {
		return CredentialProbeExecution{}, Credential{}, ErrForbidden
	}
	credential, err := s.repository.GetCredential(ctx, credentialID)
	if err != nil {
		return CredentialProbeExecution{}, Credential{}, err
	}
	model, err := s.credentialProbeModel(ctx, credential, modelID)
	if err != nil {
		return CredentialProbeExecution{}, Credential{}, err
	}
	provider, err := s.provider(ctx, credential.ProviderID)
	if err != nil {
		return CredentialProbeExecution{}, Credential{}, err
	}
	unavailable := "probe_runtime_unavailable"
	execution := CredentialProbeExecution{Kind: "generation", Status: "unavailable", ErrorKind: &unavailable, Retryable: true, MayUseTokens: true, ModelID: model.ID, ModelName: model.PublicName}
	if s.prober != nil {
		secret, secretErr := s.CredentialSecret(ctx, credentialID)
		if secretErr != nil {
			return CredentialProbeExecution{}, Credential{}, secretErr
		}
		execution = s.prober.Execute(ctx, CredentialProbeTarget{Provider: provider, Model: model, CredentialID: credentialID, Secret: secret, RequestID: requestID})
	}
	execution.RequestID = requestID
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), credentialProbePersistenceTimeout)
	defer cancel()
	credential, err = s.repository.RecordCredentialProbe(persistCtx, credentialID, time.Now().UTC(), execution, actor.UserID, requestID)
	return execution, credential, err
}

func (s *Service) credentialProbeModel(ctx context.Context, credential Credential, modelID uuid.UUID) (Model, error) {
	for _, binding := range credential.ModelBindings {
		if binding.ModelID == modelID {
			models, err := s.repository.ListModels(ctx)
			if err != nil {
				return Model{}, err
			}
			for _, model := range models {
				if model.ID == modelID && model.ProviderID == credential.ProviderID {
					return model, nil
				}
			}
		}
	}
	return Model{}, ErrInvalidInput
}

func (s *Service) ListCredentials(ctx context.Context, actor identity.Principal, includeRetired bool) ([]Credential, error) {
	if !activeAdministrator(actor) {
		return nil, ErrForbidden
	}
	return s.repository.ListCredentials(ctx, includeRetired)
}

func (s *Service) CredentialSecret(ctx context.Context, credentialID uuid.UUID) (string, error) {
	encrypted, err := s.repository.GetEncryptedCredential(ctx, credentialID)
	if err != nil {
		return "", err
	}
	plaintext, err := s.envelope.Decrypt(encrypted, CredentialEncryptionContext(credentialID))
	if err != nil {
		return "", fmt.Errorf("decrypt credential: %w", err)
	}
	return string(plaintext), nil
}

func activeAdministrator(actor identity.Principal) bool {
	return actor.Status == identity.StatusActive && actor.Role == identity.RoleAdministrator
}

func validModelIDs(modelIDs []uuid.UUID) bool {
	if len(modelIDs) == 0 || len(modelIDs) > 100 {
		return false
	}
	seen := make(map[uuid.UUID]struct{}, len(modelIDs))
	for _, modelID := range modelIDs {
		if modelID == uuid.Nil {
			return false
		}
		if _, found := seen[modelID]; found {
			return false
		}
		seen[modelID] = struct{}{}
	}
	return true
}

func validCredentialFields(name string, rpmLimit *int32, tpmLimit *int64, concurrencyLimit *int32, bindings []CredentialModelBinding) bool {
	if name == "" || utf8.RuneCountInString(name) > 120 || rpmLimit != nil && *rpmLimit < 1 || tpmLimit != nil && *tpmLimit < 1 || concurrencyLimit != nil && *concurrencyLimit < 1 || len(bindings) == 0 || len(bindings) > 100 {
		return false
	}
	seen := make(map[uuid.UUID]struct{}, len(bindings))
	for _, binding := range bindings {
		if binding.ModelID == uuid.Nil || binding.Priority < 0 || binding.Priority > 1000 || binding.Weight < 1 || binding.Weight > 1000 {
			return false
		}
		if _, found := seen[binding.ModelID]; found {
			return false
		}
		seen[binding.ModelID] = struct{}{}
	}
	return true
}

func CredentialEncryptionContext(id uuid.UUID) []byte {
	return []byte("provider-credential:" + id.String())
}
