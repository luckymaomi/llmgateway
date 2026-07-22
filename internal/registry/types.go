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
	ErrIdempotencyConflict   = errors.New("registry idempotency key conflict")
	ErrOutcomeUnknown        = errors.New("registry operation outcome is unknown")
)

type ProviderMutationAction string

const (
	ProviderMutationCreate  ProviderMutationAction = "provider.create"
	ProviderMutationUpdate  ProviderMutationAction = "provider.update"
	ProviderMutationStatus  ProviderMutationAction = "provider.status"
	ProviderMutationInstall ProviderMutationAction = "provider.install"
)

type MutationRequest struct {
	IdempotencyKey uuid.UUID
	RequestID      string
}

type ProviderMutation struct {
	Action             ProviderMutationAction
	IdempotencyKey     uuid.UUID
	RequestFingerprint []byte
	RequestID          string
}

type CredentialMutation struct {
	Action             CredentialMutationAction
	IdempotencyKey     uuid.UUID
	RequestFingerprint []byte
	RequestID          string
}

type CredentialMutationAction string

const (
	CredentialMutationCreate CredentialMutationAction = "credential.create"
	CredentialMutationUpdate CredentialMutationAction = "credential.update"
	CredentialMutationStatus CredentialMutationAction = "credential.status"
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

type ProviderPresetInstallation struct {
	PresetID string   `json:"preset_id"`
	Provider Provider `json:"provider"`
	Models   []Model  `json:"models"`
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

type CredentialModelBinding struct {
	ModelID   uuid.UUID `json:"model_id"`
	ModelName string    `json:"model_name,omitempty"`
	Priority  int32     `json:"priority"`
	Weight    int32     `json:"weight"`
}

type Credential struct {
	ID                  uuid.UUID                `json:"id"`
	ProviderID          uuid.UUID                `json:"provider_id"`
	Name                string                   `json:"name"`
	ResourceDomain      ResourceDomain           `json:"resource_domain"`
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
	CreatedAt           time.Time                `json:"created_at"`
	UpdatedAt           time.Time                `json:"updated_at"`
	ModelBindings       []CredentialModelBinding `json:"model_bindings"`
}

type CredentialChange struct {
	ID                uuid.UUID
	Name              string
	EncryptedSecret   []byte
	ReplaceSecret     bool
	ResourceDomain    ResourceDomain
	RPMLimit          *int32
	TPMLimit          *int64
	ConcurrencyLimit  *int32
	ModelBindings     []CredentialModelBinding
	ExpectedUpdatedAt time.Time
}

type CredentialProbeTarget struct {
	Provider     Provider
	Model        Model
	CredentialID uuid.UUID
	Secret       string
	RequestID    string
}

type CredentialProbeExecution struct {
	Kind          string
	Status        string
	ErrorKind     *string
	Retryable     bool
	MayUseTokens  bool
	LatencyMillis int64
	ModelID       uuid.UUID
	ModelName     string
	ResponseText  string
	InputTokens   *int64
	OutputTokens  *int64
}

type CredentialProbeExecutor interface {
	Execute(context.Context, CredentialProbeTarget) CredentialProbeExecution
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
	ModelBindings    []CredentialModelBinding
}

type Repository interface {
	ReplayProviderMutation(context.Context, uuid.UUID, ProviderMutation) (Provider, bool, error)
	ReplayProviderPresetInstallation(context.Context, uuid.UUID, ProviderMutation) (ProviderPresetInstallation, bool, error)
	InstallProviderPreset(context.Context, ProviderPresetInstallation, uuid.UUID, ProviderMutation) (ProviderPresetInstallation, error)
	CreateProvider(context.Context, Provider, uuid.UUID, ProviderMutation) (Provider, error)
	UpdateProvider(context.Context, Provider, uuid.UUID, ProviderMutation) (Provider, error)
	SetProviderEnabled(context.Context, uuid.UUID, bool, time.Time, uuid.UUID, ProviderMutation) (Provider, error)
	ListProviders(context.Context) ([]Provider, error)
	GetProvider(context.Context, uuid.UUID) (Provider, error)

	CreateModel(context.Context, Model, uuid.UUID) (Model, error)
	UpdateModel(context.Context, Model, uuid.UUID) (Model, error)
	ListModels(context.Context) ([]Model, error)

	ReplayCredentialMutation(context.Context, uuid.UUID, CredentialMutation) (Credential, bool, error)
	CreateCredential(context.Context, NewCredential, uuid.UUID, CredentialMutation) (Credential, error)
	UpdateCredential(context.Context, CredentialChange, uuid.UUID, CredentialMutation) (Credential, error)
	SetCredentialStatus(context.Context, uuid.UUID, CredentialStatus, time.Time, uuid.UUID, CredentialMutation) (Credential, error)
	ListCredentials(context.Context) ([]Credential, error)
	GetCredential(context.Context, uuid.UUID) (Credential, error)
	GetEncryptedCredential(context.Context, uuid.UUID) ([]byte, error)
	RecordCredentialProbe(context.Context, uuid.UUID, time.Time, CredentialProbeExecution, uuid.UUID, string) (Credential, error)
}
