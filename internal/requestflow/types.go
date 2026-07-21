package requestflow

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/canonical"
	"github.com/luckymaomi/llmgateway/internal/execution"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/registry"
)

var (
	ErrModelNotFound            = errors.New("model not found")
	ErrModelNotAuthorized       = errors.New("model not authorized")
	ErrNoEligibleUpstream       = errors.New("no eligible upstream")
	ErrCoordinationFailed       = errors.New("coordination unavailable")
	ErrAdmissionQueueFull       = errors.New("admission queue full")
	ErrAdmissionTimedOut        = errors.New("admission queue timed out")
	ErrAdmissionCanceled        = errors.New("admission canceled")
	ErrIdempotencyConflict      = errors.New("idempotency conflict")
	ErrQuotaExhausted           = errors.New("quota exhausted")
	ErrCostConfigurationMissing = errors.New("model cost configuration is missing")
	ErrInvalidAccounting        = errors.New("invalid accounting command")
)

type Model struct {
	ConfigRevisionID uuid.UUID
	ID               uuid.UUID
	PublicName       string
	UpstreamName     string
	ProviderID       uuid.UUID
	ProviderSlug     string
	ProviderKind     providers.Kind
	ProviderBaseURL  string
	ResourceDomain   registry.ResourceDomain
	Capabilities     registry.ModelCapabilities
	CreatedAt        time.Time
}

type Candidate struct {
	ID                  uuid.UUID
	Priority            int32
	Weight              int32
	RPMLimit            *int32
	TPMLimit            *int64
	ConcurrencyLimit    *int32
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
	Usage             *Usage
	Credential        *CredentialObservation
}

type CredentialObservationKind string

const (
	CredentialSucceeded CredentialObservationKind = "succeeded"
	CredentialFailed    CredentialObservationKind = "failed"
)

type CredentialObservation struct {
	Kind          CredentialObservationKind
	ObservedAt    time.Time
	ErrorKind     string
	CooldownUntil *time.Time
}

type CatalogRepository interface {
	ListPublishedModels(context.Context, uuid.UUID) ([]Model, error)
}

type Repository interface {
	CatalogRepository
	ResolvePublishedModel(context.Context, uuid.UUID, string) (Model, error)
	ListPublishedCandidates(context.Context, uuid.UUID, uuid.UUID, registry.ResourceDomain) ([]Candidate, error)
	ClaimExecution(context.Context, uuid.UUID, uuid.UUID) (execution.Claim, error)
	HeartbeatExecution(context.Context, execution.Claim) error
	MarkExecutionStreaming(context.Context, execution.Claim, uuid.UUID, AttemptUpdate) error
	MarkExecutionUncertain(context.Context, execution.Claim, uuid.UUID, AttemptUpdate, string, string) error
	RecoverStaleExecutions(context.Context, time.Time, int32) (int64, error)
	ListRecoverableSettlements(context.Context, time.Time, int32) ([]RecoverableSettlement, error)
	ListStaleQueuedRequests(context.Context, time.Time, int32) ([]uuid.UUID, error)
	CreateAttempt(context.Context, execution.Claim, uuid.UUID, int) (uuid.UUID, error)
	UpdateAttempt(context.Context, execution.Claim, uuid.UUID, AttemptUpdate) error
}

type RecoverableSettlement struct {
	Claim execution.Claim
	Usage Usage
}

type RecoveryResult struct {
	Settled   int64
	Released  int64
	Uncertain int64
}

type Accepted struct {
	RequestID              uuid.UUID
	ReservationID          uuid.UUID
	EntitlementID          uuid.UUID
	EntitlementConcurrency int32
	EntitlementRPMLimit    *int32
	EntitlementTPMLimit    *int64
	Existing               bool
}

type AdmissionRequest struct {
	RequestID uuid.UUID
	UserID    uuid.UUID
}

type AdmissionPermit interface {
	Release()
}

type Admitter interface {
	Acquire(context.Context, AdmissionRequest) (AdmissionPermit, time.Duration, error)
}

type AcceptCommand struct {
	RequestID        uuid.UUID
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
	Settle(context.Context, execution.Claim, Usage) error
	Release(context.Context, execution.Claim, string, string) error
	ReleaseAccepted(context.Context, uuid.UUID, string, string) error
	Compensate(context.Context, execution.Claim, Usage, string) error
}

type SecretResolver interface {
	CredentialSecret(context.Context, uuid.UUID) (string, error)
}

type LeaseRequest struct {
	RequestID              uuid.UUID
	ExecutionID            uuid.UUID
	UserID                 uuid.UUID
	GatewayKeyID           uuid.UUID
	ModelID                uuid.UUID
	ProviderID             uuid.UUID
	CredentialID           uuid.UUID
	EntitlementID          uuid.UUID
	ResourceDomain         registry.ResourceDomain
	EstimatedTokens        int64
	RPMLimit               *int32
	TPMLimit               *int64
	Concurrency            *int32
	EntitlementConcurrency int32
	EntitlementRPMLimit    *int32
	EntitlementTPMLimit    *int64
}

type Lease interface {
	Context() context.Context
	Release(context.Context) error
}

type Coordinator interface {
	Acquire(context.Context, LeaseRequest) (Lease, time.Duration, error)
}

type Observer interface {
	ProviderAttempt(providerKind providers.Kind, outcome, errorKind string)
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
	RequestID      uuid.UUID
	AcceptedSink   func(context.Context, uuid.UUID) error
	ResultSink     func(context.Context, ChatResult) error
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
