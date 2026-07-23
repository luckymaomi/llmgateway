package registry

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/providers"
)

var (
	ErrInvalidInput        = errors.New("invalid registry input")
	ErrNotFound            = errors.New("registry record not found")
	ErrConflict            = errors.New("registry conflict")
	ErrForbidden           = errors.New("registry operation forbidden")
	ErrIdempotencyConflict = errors.New("registry idempotency key conflict")
	ErrOutcomeUnknown      = errors.New("registry operation outcome is unknown")
)

type MutationRequest struct {
	IdempotencyKey uuid.UUID
	RequestID      string
}

type Mutation struct {
	Action             string
	IdempotencyKey     uuid.UUID
	RequestFingerprint []byte
	RequestID          string
}

type ModelCapabilities struct {
	Chat             bool          `json:"chat"`
	Streaming        bool          `json:"streaming"`
	Tools            bool          `json:"tools"`
	Reasoning        bool          `json:"reasoning"`
	ReasoningMode    ReasoningMode `json:"reasoning_mode,omitempty"`
	StructuredOutput bool          `json:"structured_output"`
	ContextTokens    int64         `json:"context_tokens"`
	OutputTokens     int64         `json:"output_tokens"`
}

type ReasoningMode string

const (
	ReasoningToggle ReasoningMode = "toggle"
	ReasoningEffort ReasoningMode = "effort"
	ReasoningHybrid ReasoningMode = "hybrid"
)

type Model struct {
	ID           uuid.UUID         `json:"id"`
	ProviderID   uuid.UUID         `json:"provider_id"`
	ProviderSlug string            `json:"provider_slug,omitempty"`
	ProviderName string            `json:"provider_name,omitempty"`
	PublicName   string            `json:"public_name"`
	UpstreamName string            `json:"upstream_name"`
	DisplayName  string            `json:"display_name"`
	Capabilities ModelCapabilities `json:"capabilities"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

type Provider struct {
	ID                    uuid.UUID              `json:"id"`
	CatalogID             string                 `json:"catalog_id"`
	Slug                  string                 `json:"slug"`
	Name                  string                 `json:"name"`
	Kind                  providers.Kind         `json:"kind"`
	BaseURL               string                 `json:"base_url"`
	SourceURL             string                 `json:"source_url"`
	VerifiedAt            time.Time              `json:"verified_at"`
	Contract              providers.ContractInfo `json:"contract"`
	ResourcePoolCount     int64                  `json:"resource_pool_count"`
	ActiveCredentialCount int64                  `json:"active_credential_count"`
	CreatedAt             time.Time              `json:"created_at"`
	UpdatedAt             time.Time              `json:"updated_at"`
}

type ProviderProjection struct {
	CatalogID  string
	Slug       string
	Name       string
	Kind       providers.Kind
	BaseURL    string
	SourceURL  string
	VerifiedAt time.Time
	Models     []ModelProjection
}

type ModelProjection struct {
	PublicName   string
	UpstreamName string
	DisplayName  string
	Capabilities ModelCapabilities
}

type ResourcePoolStatus string

const (
	ResourcePoolActive   ResourcePoolStatus = "active"
	ResourcePoolDisabled ResourcePoolStatus = "disabled"
	ResourcePoolRetired  ResourcePoolStatus = "retired"
)

type ResourcePool struct {
	ID                    uuid.UUID          `json:"id"`
	ProviderID            uuid.UUID          `json:"provider_id"`
	ProviderCatalogID     string             `json:"provider_catalog_id"`
	ProviderSlug          string             `json:"provider_slug"`
	ProviderName          string             `json:"provider_name"`
	ProviderKind          providers.Kind     `json:"provider_kind"`
	ProviderBaseURL       string             `json:"provider_base_url"`
	Slug                  string             `json:"slug"`
	Name                  string             `json:"name"`
	Status                ResourcePoolStatus `json:"status"`
	Models                []Model            `json:"models"`
	ModelCount            int64              `json:"model_count"`
	CredentialCount       int64              `json:"credential_count"`
	ActiveCredentialCount int64              `json:"active_credential_count"`
	RetiredAt             *time.Time         `json:"retired_at,omitempty"`
	CreatedAt             time.Time          `json:"created_at"`
	UpdatedAt             time.Time          `json:"updated_at"`
}

type NewResourcePool struct {
	ProviderID uuid.UUID
	Slug       string
	Name       string
	ModelIDs   []uuid.UUID
}

type ResourcePoolChange struct {
	ID                uuid.UUID
	Name              string
	ExpectedUpdatedAt time.Time
}

type CredentialStatus string

const (
	CredentialActive   CredentialStatus = "active"
	CredentialCooling  CredentialStatus = "cooling"
	CredentialDisabled CredentialStatus = "disabled"
	CredentialRetired  CredentialStatus = "retired"
)

type CredentialModelBinding struct {
	ModelID   uuid.UUID `json:"model_id"`
	ModelName string    `json:"model_name,omitempty"`
	Priority  int32     `json:"priority"`
	Weight    int32     `json:"weight"`
}

type Credential struct {
	ID                  uuid.UUID                `json:"id"`
	ResourcePoolID      uuid.UUID                `json:"resource_pool_id"`
	ResourcePoolName    string                   `json:"resource_pool_name"`
	ResourcePoolSlug    string                   `json:"resource_pool_slug"`
	ProviderID          uuid.UUID                `json:"provider_id"`
	ProviderName        string                   `json:"provider_name"`
	ProviderKind        providers.Kind           `json:"provider_kind"`
	ProviderBaseURL     string                   `json:"provider_base_url"`
	Name                string                   `json:"name"`
	Status              CredentialStatus         `json:"status"`
	RPMLimit            *int32                   `json:"rpm_limit,omitempty"`
	TPMLimit            *int64                   `json:"tpm_limit,omitempty"`
	ConcurrencyLimit    *int32                   `json:"concurrency_limit,omitempty"`
	CooldownUntil       *time.Time               `json:"cooldown_until,omitempty"`
	ConsecutiveFailures int32                    `json:"consecutive_failures"`
	LastSuccessAt       *time.Time               `json:"last_success_at,omitempty"`
	LastErrorKind       *string                  `json:"last_error_kind,omitempty"`
	LastProbeAt         *time.Time               `json:"last_probe_at,omitempty"`
	LastProbeLatencyMs  *int64                   `json:"last_probe_latency_ms,omitempty"`
	LastProbeKind       *string                  `json:"last_probe_kind,omitempty"`
	LastProbeStatus     *string                  `json:"last_probe_status,omitempty"`
	LastProbeErrorKind  *string                  `json:"last_probe_error_kind,omitempty"`
	LastCheckedAt       *time.Time               `json:"last_checked_at,omitempty"`
	RecentSuccessRate   *float64                 `json:"recent_success_rate,omitempty"`
	FirstByteP95Ms      *int64                   `json:"first_byte_p95_ms,omitempty"`
	TotalLatencyP95Ms   *int64                   `json:"total_latency_p95_ms,omitempty"`
	RetiredAt           *time.Time               `json:"retired_at,omitempty"`
	CreatedAt           time.Time                `json:"created_at"`
	UpdatedAt           time.Time                `json:"updated_at"`
	ModelBindings       []CredentialModelBinding `json:"model_bindings"`
}

type NewCredential struct {
	ID               uuid.UUID
	ResourcePoolID   uuid.UUID
	Name             string
	EncryptedSecret  []byte
	RPMLimit         *int32
	TPMLimit         *int64
	ConcurrencyLimit *int32
	ModelBindings    []CredentialModelBinding
}

type CredentialChange struct {
	ID                uuid.UUID
	Name              string
	EncryptedSecret   []byte
	ReplaceSecret     bool
	RPMLimit          *int32
	TPMLimit          *int64
	ConcurrencyLimit  *int32
	ModelBindings     []CredentialModelBinding
	ExpectedUpdatedAt time.Time
}

type CredentialBatchItem struct {
	Name   string `json:"name"`
	Secret string `json:"-"`
}

type CredentialBatchResult struct {
	Line       int         `json:"line"`
	Name       string      `json:"name"`
	Status     string      `json:"status"`
	Credential *Credential `json:"credential,omitempty"`
	ErrorKind  string      `json:"error_kind,omitempty"`
}

type CredentialProbeTarget struct {
	Provider     Provider
	Model        Model
	CredentialID uuid.UUID
	Secret       string
	RequestID    string
}

type CredentialProbeExecution struct {
	Kind          string    `json:"kind"`
	Status        string    `json:"status"`
	ErrorKind     *string   `json:"error_kind,omitempty"`
	Retryable     bool      `json:"retryable"`
	MayUseTokens  bool      `json:"may_use_tokens"`
	LatencyMillis int64     `json:"latency_ms"`
	ModelID       uuid.UUID `json:"model_id"`
	ModelName     string    `json:"model_name"`
	RequestID     string    `json:"request_id"`
	ResponseText  string    `json:"-"`
	InputTokens   *int64    `json:"input_tokens,omitempty"`
	OutputTokens  *int64    `json:"output_tokens,omitempty"`
}

type CredentialProbeExecutor interface {
	Execute(context.Context, CredentialProbeTarget) CredentialProbeExecution
}

type Repository interface {
	SyncCatalog(context.Context, []ProviderProjection) error
	ListProviders(context.Context) ([]Provider, error)
	GetProvider(context.Context, uuid.UUID) (Provider, error)
	ListModels(context.Context) ([]Model, error)

	CreateResourcePool(context.Context, NewResourcePool, uuid.UUID, Mutation) (ResourcePool, error)
	UpdateResourcePool(context.Context, ResourcePoolChange, uuid.UUID, Mutation) (ResourcePool, error)
	SetResourcePoolStatus(context.Context, uuid.UUID, ResourcePoolStatus, time.Time, uuid.UUID, Mutation) (ResourcePool, error)
	ListResourcePools(context.Context, bool) ([]ResourcePool, error)
	GetResourcePool(context.Context, uuid.UUID) (ResourcePool, error)

	CreateCredential(context.Context, NewCredential, uuid.UUID, Mutation) (Credential, error)
	UpdateCredential(context.Context, CredentialChange, uuid.UUID, Mutation) (Credential, error)
	SetCredentialStatus(context.Context, uuid.UUID, CredentialStatus, time.Time, uuid.UUID, Mutation) (Credential, error)
	RetireCredential(context.Context, uuid.UUID, []byte, time.Time, uuid.UUID, Mutation) (Credential, error)
	ListCredentials(context.Context, bool) ([]Credential, error)
	GetCredential(context.Context, uuid.UUID) (Credential, error)
	GetEncryptedCredential(context.Context, uuid.UUID) ([]byte, error)
	RecordCredentialProbe(context.Context, uuid.UUID, time.Time, CredentialProbeExecution, uuid.UUID, string) (Credential, error)
}
