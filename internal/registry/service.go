package registry

import (
	"context"
	"encoding/json"
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

type Service struct {
	repository Repository
	envelope   *security.EnvelopeCipher
	urls       *security.URLValidator
}

func NewService(repository Repository, envelope *security.EnvelopeCipher, urls *security.URLValidator) (*Service, error) {
	if repository == nil || envelope == nil || urls == nil {
		return nil, fmt.Errorf("registry dependencies are required")
	}
	return &Service{repository: repository, envelope: envelope, urls: urls}, nil
}

func (s *Service) CreateProvider(ctx context.Context, actor identity.Principal, provider Provider) (Provider, error) {
	if !actor.CanOperateProviders() {
		return Provider{}, ErrForbidden
	}
	provider.ID = uuid.New()
	if err := s.validateProvider(ctx, provider); err != nil {
		return Provider{}, err
	}
	return s.repository.CreateProvider(ctx, provider, actor.UserID)
}

func (s *Service) UpdateProvider(ctx context.Context, actor identity.Principal, provider Provider) (Provider, error) {
	if !actor.CanOperateProviders() {
		return Provider{}, ErrForbidden
	}
	if provider.ID == uuid.Nil {
		return Provider{}, ErrInvalidInput
	}
	if err := s.validateProvider(ctx, provider); err != nil {
		return Provider{}, err
	}
	return s.repository.UpdateProvider(ctx, provider, actor.UserID)
}

func (s *Service) ListProviders(ctx context.Context, actor identity.Principal) ([]Provider, error) {
	if !actor.CanOperateProviders() {
		return nil, ErrForbidden
	}
	return s.repository.ListProviders(ctx)
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

func (s *Service) CreateCredential(ctx context.Context, actor identity.Principal, input NewCredential, secret string) (Credential, error) {
	if !actor.CanOperateProviders() {
		return Credential{}, ErrForbidden
	}
	input.ID = uuid.New()
	input.Name = strings.TrimSpace(input.Name)
	if input.ProviderID == uuid.Nil || input.Name == "" || utf8.RuneCountInString(input.Name) > 80 || len(secret) < 8 || len(secret) > 8192 {
		return Credential{}, ErrInvalidInput
	}
	if input.ResourceDomain != ResourceFree && input.ResourceDomain != ResourceProfessional {
		return Credential{}, ErrInvalidInput
	}
	if input.RPMLimit != nil && *input.RPMLimit < 1 || input.TPMLimit != nil && *input.TPMLimit < 1 || input.ConcurrencyLimit != nil && *input.ConcurrencyLimit < 1 {
		return Credential{}, ErrInvalidInput
	}
	if input.FixedProxyURL != nil {
		trimmed := strings.TrimSpace(*input.FixedProxyURL)
		if _, err := s.urls.ValidateString(ctx, trimmed); err != nil {
			return Credential{}, fmt.Errorf("%w: fixed proxy URL", ErrInvalidInput)
		}
		input.FixedProxyURL = &trimmed
	}
	encrypted, err := s.envelope.Encrypt([]byte(secret), credentialAAD(input.ID))
	if err != nil {
		return Credential{}, fmt.Errorf("encrypt credential: %w", err)
	}
	input.EncryptedSecret = encrypted
	return s.repository.CreateCredential(ctx, input, actor.UserID)
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
	plaintext, err := s.envelope.Decrypt(encrypted, credentialAAD(credentialID))
	if err != nil {
		return "", fmt.Errorf("decrypt credential: %w", err)
	}
	return string(plaintext), nil
}

func (s *Service) BindCredentialModel(ctx context.Context, actor identity.Principal, credentialID, modelID uuid.UUID, priority, weight int32) error {
	if !actor.CanOperateProviders() {
		return ErrForbidden
	}
	if credentialID == uuid.Nil || modelID == uuid.Nil || priority < 0 || weight < 1 || weight > 10000 {
		return ErrInvalidInput
	}
	return s.repository.BindCredentialModel(ctx, credentialID, modelID, priority, weight, actor.UserID)
}

func (s *Service) validateProvider(ctx context.Context, provider Provider) error {
	provider.Slug = strings.TrimSpace(provider.Slug)
	provider.Name = strings.TrimSpace(provider.Name)
	if !slugPattern.MatchString(provider.Slug) || provider.Name == "" || utf8.RuneCountInString(provider.Name) > 100 {
		return ErrInvalidInput
	}
	switch provider.Kind {
	case providers.KindOpenAICompatible, providers.KindDeepSeek, providers.KindZhipu, providers.KindAgnes:
	default:
		return ErrInvalidInput
	}
	if _, err := s.urls.ValidateString(ctx, provider.BaseURL); err != nil {
		return fmt.Errorf("%w: provider base URL", ErrInvalidInput)
	}
	if provider.SourceURL != nil {
		if _, err := s.urls.ValidateString(ctx, *provider.SourceURL); err != nil {
			return fmt.Errorf("%w: provider source URL", ErrInvalidInput)
		}
	}
	if provider.VerifiedAt != nil && provider.VerifiedAt.After(time.Now().UTC().Add(time.Minute)) {
		return ErrInvalidInput
	}
	return nil
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
	if _, err := json.Marshal(model.Capabilities); err != nil {
		return ErrInvalidInput
	}
	return nil
}

func credentialAAD(id uuid.UUID) []byte {
	return []byte("provider-credential:" + id.String())
}
