package registry

import (
	"context"
	"encoding/json"
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
	urls       *security.URLValidator
	prober     CredentialProbeExecutor
}

func NewService(repository Repository, envelope *security.EnvelopeCipher, urls *security.URLValidator) (*Service, error) {
	if repository == nil || envelope == nil || urls == nil {
		return nil, fmt.Errorf("registry dependencies are required")
	}
	return &Service{repository: repository, envelope: envelope, urls: urls}, nil
}

func (s *Service) WithCredentialProbeExecutor(prober CredentialProbeExecutor) *Service {
	s.prober = prober
	return s
}

func (s *Service) CreateProvider(ctx context.Context, actor identity.Principal, provider Provider, request MutationRequest) (Provider, error) {
	if !actor.CanOperateProviders() {
		return Provider{}, ErrForbidden
	}
	provider.ID = uuid.New()
	provider.Enabled = false
	provider = normalizeProvider(provider)
	mutation, err := createProviderMutation(request, provider)
	if err != nil {
		return Provider{}, err
	}
	if replayed, found, err := s.repository.ReplayProviderMutation(ctx, actor.UserID, mutation); err != nil || found {
		return replayed, err
	}
	if !slugPattern.MatchString(provider.Slug) {
		return Provider{}, ErrInvalidInput
	}
	if err := s.validateProviderDetails(ctx, provider); err != nil {
		return Provider{}, err
	}
	if err := s.validateProviderSource(ctx, provider); err != nil {
		return Provider{}, err
	}
	return s.repository.CreateProvider(ctx, provider, actor.UserID, mutation)
}

func (s *Service) UpdateProvider(ctx context.Context, actor identity.Principal, provider Provider, request MutationRequest) (Provider, error) {
	if !actor.CanOperateProviders() {
		return Provider{}, ErrForbidden
	}
	if provider.ID == uuid.Nil || provider.UpdatedAt.IsZero() {
		return Provider{}, ErrInvalidInput
	}
	provider = normalizeProvider(provider)
	mutation, err := updateProviderMutation(request, provider)
	if err != nil {
		return Provider{}, err
	}
	if replayed, found, err := s.repository.ReplayProviderMutation(ctx, actor.UserID, mutation); err != nil || found {
		return replayed, err
	}
	if err := s.validateProviderDetails(ctx, provider); err != nil {
		return Provider{}, err
	}
	return s.repository.UpdateProvider(ctx, provider, actor.UserID, mutation)
}

func (s *Service) SetProviderEnabled(ctx context.Context, actor identity.Principal, providerID uuid.UUID, enabled bool, expectedUpdatedAt time.Time, request MutationRequest) (Provider, error) {
	if !actor.CanOperateProviders() {
		return Provider{}, ErrForbidden
	}
	if providerID == uuid.Nil || expectedUpdatedAt.IsZero() {
		return Provider{}, ErrInvalidInput
	}
	mutation, err := statusProviderMutation(request, providerID, enabled, expectedUpdatedAt)
	if err != nil {
		return Provider{}, err
	}
	if replayed, found, err := s.repository.ReplayProviderMutation(ctx, actor.UserID, mutation); err != nil || found {
		return replayed, err
	}
	return s.repository.SetProviderEnabled(ctx, providerID, enabled, expectedUpdatedAt, actor.UserID, mutation)
}

func (s *Service) ListProviders(ctx context.Context, actor identity.Principal) ([]Provider, error) {
	if !actor.CanOperateProviders() {
		return nil, ErrForbidden
	}
	return s.repository.ListProviders(ctx)
}

func (s *Service) GetProvider(ctx context.Context, actor identity.Principal, providerID uuid.UUID) (Provider, error) {
	if !actor.CanOperateProviders() {
		return Provider{}, ErrForbidden
	}
	if providerID == uuid.Nil {
		return Provider{}, ErrInvalidInput
	}
	return s.repository.GetProvider(ctx, providerID)
}

func (s *Service) CreateModel(ctx context.Context, actor identity.Principal, model Model) (Model, error) {
	if !actor.CanOperateProviders() {
		return Model{}, ErrForbidden
	}
	model.ID = uuid.New()
	if err := validateModel(model); err != nil {
		return Model{}, err
	}
	return s.repository.CreateModel(ctx, model, actor.UserID)
}

func (s *Service) UpdateModel(ctx context.Context, actor identity.Principal, model Model) (Model, error) {
	if !actor.CanOperateProviders() {
		return Model{}, ErrForbidden
	}
	if model.ID == uuid.Nil {
		return Model{}, ErrInvalidInput
	}
	if err := validateModel(model); err != nil {
		return Model{}, err
	}
	return s.repository.UpdateModel(ctx, model, actor.UserID)
}

func (s *Service) ListModels(ctx context.Context, actor identity.Principal) ([]Model, error) {
	if actor.Status != identity.StatusActive {
		return nil, ErrForbidden
	}
	return s.repository.ListModels(ctx)
}

func (s *Service) CreateCredential(ctx context.Context, actor identity.Principal, input NewCredential, secret string, request MutationRequest) (Credential, error) {
	if !actor.CanOperateProviders() {
		return Credential{}, ErrForbidden
	}
	input.ID = uuid.New()
	input.Name = strings.TrimSpace(input.Name)
	if input.ProviderID == uuid.Nil || len(secret) < 8 || len(secret) > 8192 || !validCredentialFields(input.Name, input.ResourceDomain, input.RPMLimit, input.TPMLimit, input.ConcurrencyLimit, input.ModelBindings) {
		return Credential{}, ErrInvalidInput
	}
	mutation, err := newCredentialMutation(request, input, secret)
	if err != nil {
		return Credential{}, err
	}
	if replayed, found, err := s.repository.ReplayCredentialMutation(ctx, actor.UserID, mutation); err != nil || found {
		return replayed, err
	}
	encrypted, err := s.envelope.Encrypt([]byte(secret), CredentialEncryptionContext(input.ID))
	if err != nil {
		return Credential{}, fmt.Errorf("encrypt credential: %w", err)
	}
	input.EncryptedSecret = encrypted
	return s.repository.CreateCredential(ctx, input, actor.UserID, mutation)
}

func (s *Service) UpdateCredential(ctx context.Context, actor identity.Principal, input CredentialChange, secret string, request MutationRequest) (Credential, error) {
	if !actor.CanOperateProviders() {
		return Credential{}, ErrForbidden
	}
	input.Name = strings.TrimSpace(input.Name)
	input.ReplaceSecret = secret != ""
	if input.ID == uuid.Nil || input.ExpectedUpdatedAt.IsZero() || !validCredentialFields(input.Name, input.ResourceDomain, input.RPMLimit, input.TPMLimit, input.ConcurrencyLimit, input.ModelBindings) {
		return Credential{}, ErrInvalidInput
	}
	if input.ReplaceSecret && (len(secret) < 8 || len(secret) > 8192) {
		return Credential{}, ErrInvalidInput
	}
	mutation, err := updateCredentialMutation(request, input, secret)
	if err != nil {
		return Credential{}, err
	}
	if replayed, found, err := s.repository.ReplayCredentialMutation(ctx, actor.UserID, mutation); err != nil || found {
		return replayed, err
	}
	if input.ReplaceSecret {
		encrypted, err := s.envelope.Encrypt([]byte(secret), CredentialEncryptionContext(input.ID))
		if err != nil {
			return Credential{}, fmt.Errorf("encrypt credential: %w", err)
		}
		input.EncryptedSecret = encrypted
	}
	return s.repository.UpdateCredential(ctx, input, actor.UserID, mutation)
}

func (s *Service) SetCredentialEnabled(ctx context.Context, actor identity.Principal, credentialID uuid.UUID, enabled bool, expectedUpdatedAt time.Time, request MutationRequest) (Credential, error) {
	if !actor.CanOperateProviders() {
		return Credential{}, ErrForbidden
	}
	if credentialID == uuid.Nil || expectedUpdatedAt.IsZero() {
		return Credential{}, ErrInvalidInput
	}
	status := CredentialDisabled
	if enabled {
		status = CredentialActive
	}
	mutation, err := statusCredentialMutation(request, credentialID, status, expectedUpdatedAt)
	if err != nil {
		return Credential{}, err
	}
	if replayed, found, err := s.repository.ReplayCredentialMutation(ctx, actor.UserID, mutation); err != nil || found {
		return replayed, err
	}
	return s.repository.SetCredentialStatus(ctx, credentialID, status, expectedUpdatedAt, actor.UserID, mutation)
}

func (s *Service) ProbeCredential(ctx context.Context, actor identity.Principal, credentialID, modelID uuid.UUID, requestID string) (CredentialProbeExecution, Credential, error) {
	if !actor.CanOperateProviders() {
		return CredentialProbeExecution{}, Credential{}, ErrForbidden
	}
	if credentialID == uuid.Nil || modelID == uuid.Nil || requestID == "" || len(requestID) > 128 {
		return CredentialProbeExecution{}, Credential{}, ErrInvalidInput
	}
	credential, err := s.repository.GetCredential(ctx, credentialID)
	if err != nil {
		return CredentialProbeExecution{}, Credential{}, err
	}
	provider, err := s.repository.GetProvider(ctx, credential.ProviderID)
	if err != nil {
		return CredentialProbeExecution{}, Credential{}, err
	}
	model, err := s.credentialProbeModel(ctx, credential, modelID)
	if err != nil {
		return CredentialProbeExecution{}, Credential{}, err
	}
	execution := CredentialProbeExecution{Kind: "generation", Status: "unavailable", MayUseTokens: true, ModelID: model.ID, ModelName: model.PublicName}
	unavailable := "probe_runtime_unavailable"
	execution.ErrorKind = &unavailable
	if s.prober != nil {
		secret, err := s.CredentialSecret(ctx, credentialID)
		if err != nil {
			return CredentialProbeExecution{}, Credential{}, err
		}
		execution = s.prober.Execute(ctx, CredentialProbeTarget{
			Provider: provider, Model: model, CredentialID: credentialID, Secret: secret, RequestID: requestID,
		})
	}
	checkedAt := time.Now().UTC()
	persistenceContext, cancelPersistence := context.WithTimeout(context.WithoutCancel(ctx), credentialProbePersistenceTimeout)
	defer cancelPersistence()
	credential, err = s.repository.RecordCredentialProbe(persistenceContext, credentialID, checkedAt, execution, actor.UserID, requestID)
	return execution, credential, err
}

func (s *Service) credentialProbeModel(ctx context.Context, credential Credential, modelID uuid.UUID) (Model, error) {
	bound := false
	for _, binding := range credential.ModelBindings {
		if binding.ModelID == modelID {
			bound = true
			break
		}
	}
	if !bound {
		return Model{}, ErrInvalidInput
	}
	models, err := s.repository.ListModels(ctx)
	if err != nil {
		return Model{}, err
	}
	for _, model := range models {
		if model.ID == modelID && model.ProviderID == credential.ProviderID {
			return model, nil
		}
	}
	return Model{}, ErrInvalidInput
}

func (s *Service) ListCredentials(ctx context.Context, actor identity.Principal) ([]Credential, error) {
	if !actor.CanOperateProviders() {
		return nil, ErrForbidden
	}
	return s.repository.ListCredentials(ctx)
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

func (s *Service) validateProviderDetails(ctx context.Context, provider Provider) error {
	nameRunes := utf8.RuneCountInString(provider.Name)
	if nameRunes < 2 || nameRunes > 100 || len(provider.BaseURL) > 2048 {
		return ErrInvalidInput
	}
	if !providers.DefaultCatalog().Supports(provider.Kind) {
		return ErrInvalidInput
	}
	baseURL, err := s.urls.ValidateString(ctx, provider.BaseURL)
	if errors.Is(err, security.ErrURLResolution) {
		return ErrValidationUnavailable
	}
	if err != nil || baseURL.Scheme != "https" || baseURL.ForceQuery || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return fmt.Errorf("%w: provider base URL", ErrInvalidInput)
	}
	return nil
}

func (s *Service) validateProviderSource(ctx context.Context, provider Provider) error {
	if provider.SourceURL != nil {
		if len(*provider.SourceURL) > 2048 {
			return ErrInvalidInput
		}
		sourceURL, err := s.urls.ValidateString(ctx, *provider.SourceURL)
		if errors.Is(err, security.ErrURLResolution) {
			return ErrValidationUnavailable
		}
		if err != nil || sourceURL.Scheme != "https" || sourceURL.ForceQuery || sourceURL.RawQuery != "" || sourceURL.Fragment != "" {
			return fmt.Errorf("%w: provider source URL", ErrInvalidInput)
		}
	}
	if provider.VerifiedAt != nil && provider.VerifiedAt.After(time.Now().UTC().Add(time.Minute)) {
		return ErrInvalidInput
	}
	return nil
}

func normalizeProvider(provider Provider) Provider {
	provider.Slug = strings.TrimSpace(provider.Slug)
	provider.Name = strings.TrimSpace(provider.Name)
	provider.BaseURL = strings.TrimSpace(provider.BaseURL)
	if provider.SourceURL != nil {
		value := strings.TrimSpace(*provider.SourceURL)
		provider.SourceURL = &value
	}
	return provider
}

func validateModel(model Model) error {
	if model.ProviderID == uuid.Nil || strings.TrimSpace(model.PublicName) == "" || strings.TrimSpace(model.UpstreamName) == "" || strings.TrimSpace(model.DisplayName) == "" {
		return ErrInvalidInput
	}
	if model.ResourceDomain != ResourceFree && model.ResourceDomain != ResourceProfessional {
		return ErrInvalidInput
	}
	if !model.Capabilities.Chat || model.Capabilities.ContextTokens < 1 || model.Capabilities.OutputTokens < 1 || model.Capabilities.OutputTokens > model.Capabilities.ContextTokens {
		return ErrInvalidInput
	}
	if model.Capabilities.Reasoning != (model.Capabilities.ReasoningMode != "") {
		return ErrInvalidInput
	}
	switch model.Capabilities.ReasoningMode {
	case "", ReasoningToggle, ReasoningEffort, ReasoningHybrid:
	default:
		return ErrInvalidInput
	}
	if _, err := json.Marshal(model.Capabilities); err != nil {
		return ErrInvalidInput
	}
	return nil
}

func validCredentialFields(name string, domain ResourceDomain, rpmLimit *int32, tpmLimit *int64, concurrencyLimit *int32, bindings []CredentialModelBinding) bool {
	if name == "" || utf8.RuneCountInString(name) > 80 || domain != ResourceFree && domain != ResourceProfessional {
		return false
	}
	if rpmLimit != nil && *rpmLimit < 1 || tpmLimit != nil && *tpmLimit < 1 || concurrencyLimit != nil && *concurrencyLimit < 1 {
		return false
	}
	if len(bindings) == 0 || len(bindings) > 100 {
		return false
	}
	seen := make(map[uuid.UUID]struct{}, len(bindings))
	for _, binding := range bindings {
		if binding.ModelID == uuid.Nil || binding.Priority < 0 || binding.Priority > 1000 || binding.Weight < 1 || binding.Weight > 1000 {
			return false
		}
		if _, exists := seen[binding.ModelID]; exists {
			return false
		}
		seen[binding.ModelID] = struct{}{}
	}
	return true
}

func CredentialEncryptionContext(id uuid.UUID) []byte {
	return []byte("provider-credential:" + id.String())
}
