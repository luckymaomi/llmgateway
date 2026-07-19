package requestflow

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/canonical"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/registry"
)

var (
	ErrModelNotFound       = errors.New("model not found")
	ErrModelNotAuthorized  = errors.New("model not authorized")
	ErrNoEligibleUpstream  = errors.New("no eligible upstream")
	ErrCoordinationFailed  = errors.New("coordination unavailable")
	ErrIdempotencyConflict = errors.New("idempotency conflict")
)

type Model struct {
	ID              uuid.UUID
	PublicName      string
	UpstreamName    string
	ProviderID      uuid.UUID
	ProviderSlug    string
	ProviderKind    providers.Kind
	ProviderBaseURL string
	ResourceDomain  registry.ResourceDomain
	Capabilities    registry.ModelCapabilities
	CreatedAt       time.Time
}

type Candidate struct {
	ID                  uuid.UUID
	Priority            int32
	Weight              int32
	RPMLimit            *int32
	TPMLimit            *int64
	ConcurrencyLimit    *int32
	FixedProxyURL       *string
	ConsecutiveFailures int32
	LastSuccessAt       *time.Time
	CooldownUntil       *time.Time
}

type AttemptUpdate struct {
	Status            string
	HTTPStatus        *int
	UpstreamRequestID *string
	ErrorKind         *string
	RetryAfterAt      *time.Time
	SentAt            *time.Time
	FirstByteAt       *time.Time
	CompletedAt       *time.Time
}

type Repository interface {
	ListAuthorizedModels(context.Context, uuid.UUID) ([]Model, error)
	ResolveAuthorizedModel(context.Context, uuid.UUID, string) (Model, error)
	ListCandidates(context.Context, uuid.UUID, registry.ResourceDomain) ([]Candidate, error)
	ActiveConfigRevision(context.Context) (*uuid.UUID, error)
	CreateAttempt(context.Context, uuid.UUID, uuid.UUID, int) (uuid.UUID, error)
	UpdateAttempt(context.Context, uuid.UUID, AttemptUpdate) error
}

type Accepted struct {
	RequestID     uuid.UUID
	ReservationID uuid.UUID
	Existing      bool
}

type AcceptCommand struct {
	UserID           uuid.UUID
	GatewayKeyID     uuid.UUID
	ModelID          uuid.UUID
	ResourceDomain   registry.ResourceDomain
	ConfigRevisionID *uuid.UUID
	IdempotencyKey   *string
	RequestDigest    []byte
	Stream           bool
	ReservedTokens   int64
}

type Usage struct {
	InputTokens  int64
	OutputTokens int64
	Source       canonical.UsageSource
}

type Accounting interface {
	AcceptRequest(context.Context, AcceptCommand) (Accepted, error)
	Settle(context.Context, uuid.UUID, Usage) error
	Release(context.Context, uuid.UUID, string, string) error
	Compensate(context.Context, uuid.UUID, Usage, string) error
}

type SecretResolver interface {
	CredentialSecret(context.Context, uuid.UUID) (string, error)
}

type LeaseRequest struct {
	RequestID       uuid.UUID
	UserID          uuid.UUID
	GatewayKeyID    uuid.UUID
	ModelID         uuid.UUID
	ProviderID      uuid.UUID
	CredentialID    uuid.UUID
	EstimatedTokens int64
	RPMLimit        *int32
	TPMLimit        *int64
	Concurrency     *int32
}

type Lease interface {
	Release(context.Context) error
}

type Coordinator interface {
	Acquire(context.Context, LeaseRequest) (Lease, time.Duration, error)
}

type AdapterFactory interface {
	Adapter(Model) (providers.Adapter, error)
	Client(Candidate) (*http.Client, error)
}

type ChatCommand struct {
	Principal      identity.GatewayPrincipal
	Request        canonical.ChatRequest
	RequestDigest  []byte
	IdempotencyKey *string
}

type ChatResult struct {
	RequestID uuid.UUID
	Response  canonical.ChatResponse
}

type StreamSink func(uuid.UUID, canonical.StreamEvent) error

type Clock interface {
	Now() time.Time
}

type Random interface {
	Intn(int) int
}
