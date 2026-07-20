package registry

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/providers"
)

var (
	ErrInvalidInput          = errors.New("invalid registry input")
	ErrNotFound              = errors.New("registry record not found")
	ErrConflict              = errors.New("registry conflict")
	ErrForbidden             = errors.New("registry operation forbidden")
	ErrProviderEnabled       = errors.New("provider must be disabled before changing routing fields")
	ErrValidationUnavailable = errors.New("registry validation is temporarily unavailable")
)

type ResourceDomain string

const (
	ResourceFree         ResourceDomain = "free"
	ResourceProfessional ResourceDomain = "professional"
)

type Provider struct {
	ID         uuid.UUID      `json:"id"`
	Slug       string         `json:"slug"`
	Name       string         `json:"name"`
	Kind       providers.Kind `json:"kind"`
	BaseURL    string         `json:"base_url"`
	Enabled    bool           `json:"enabled"`
	SourceURL  *string        `json:"source_url,omitempty"`
	VerifiedAt *time.Time     `json:"verified_at,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

type ModelCapabilities struct {
	Chat             bool  `json:"chat"`
	Streaming        bool  `json:"streaming"`
	Tools            bool  `json:"tools"`
	Reasoning        bool  `json:"reasoning"`
	StructuredOutput bool  `json:"structured_output"`
	ContextTokens    int64 `json:"context_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
}

type Model struct {
	ID             uuid.UUID         `json:"id"`
	ProviderID     uuid.UUID         `json:"provider_id"`
	ProviderSlug   string            `json:"provider_slug,omitempty"`
	ProviderName   string            `json:"provider_name,omitempty"`
	PublicName     string            `json:"public_name"`
	UpstreamName   string            `json:"upstream_name"`
	DisplayName    string            `json:"display_name"`
	ResourceDomain ResourceDomain    `json:"resource_domain"`
	Capabilities   ModelCapabilities `json:"capabilities"`
	Enabled        bool              `json:"enabled"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

type CredentialStatus string

const (
	CredentialActive   CredentialStatus = "active"
	CredentialCooling  CredentialStatus = "cooling"
	CredentialDisabled CredentialStatus = "disabled"
)

type Credential struct {
	ID                  uuid.UUID        `json:"id"`
	ProviderID          uuid.UUID        `json:"provider_id"`
	Name                string           `json:"name"`
	ResourceDomain      ResourceDomain   `json:"resource_domain"`
	Status              CredentialStatus `json:"status"`
	RPMLimit            *int32           `json:"rpm_limit,omitempty"`
	TPMLimit            *int64           `json:"tpm_limit,omitempty"`
	ConcurrencyLimit    *int32           `json:"concurrency_limit,omitempty"`
	FixedProxyURL       *string          `json:"fixed_proxy_url,omitempty"`
	CooldownUntil       *time.Time       `json:"cooldown_until,omitempty"`
	ConsecutiveFailures int32            `json:"consecutive_failures"`
	LastSuccessAt       *time.Time       `json:"last_success_at,omitempty"`
	LastErrorKind       *string          `json:"last_error_kind,omitempty"`
	CreatedAt           time.Time        `json:"created_at"`
	UpdatedAt           time.Time        `json:"updated_at"`
}

type NewCredential struct {
	ID               uuid.UUID
	ProviderID       uuid.UUID
	Name             string
	EncryptedSecret  []byte
	ResourceDomain   ResourceDomain
	RPMLimit         *int32
	TPMLimit         *int64
	ConcurrencyLimit *int32
	FixedProxyURL    *string
}

type Repository interface {
	CreateProvider(context.Context, Provider, uuid.UUID) (Provider, error)
	UpdateProvider(context.Context, Provider, uuid.UUID) (Provider, error)
	SetProviderEnabled(context.Context, uuid.UUID, bool, time.Time, uuid.UUID) (Provider, error)
	ListProviders(context.Context) ([]Provider, error)
	GetProvider(context.Context, uuid.UUID) (Provider, error)

	CreateModel(context.Context, Model, uuid.UUID) (Model, error)
	UpdateModel(context.Context, Model, uuid.UUID) (Model, error)
	ListModels(context.Context) ([]Model, error)

	CreateCredential(context.Context, NewCredential, uuid.UUID) (Credential, error)
	ListCredentials(context.Context) ([]Credential, error)
	GetEncryptedCredential(context.Context, uuid.UUID) ([]byte, error)
	BindCredentialModel(context.Context, uuid.UUID, uuid.UUID, int32, int32, uuid.UUID) error
}
