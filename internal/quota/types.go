package quota

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/execution"
	"github.com/luckymaomi/llmgateway/internal/identity"
)

var (
	ErrInvalidInput             = errors.New("invalid quota input")
	ErrForbidden                = errors.New("quota operation forbidden")
	ErrNotFound                 = errors.New("quota record not found")
	ErrConflict                 = errors.New("quota conflict")
	ErrModelNotAuthorized       = errors.New("model is not authorized")
	ErrQuotaExhausted           = errors.New("quota exhausted")
	ErrCostConfigurationMissing = errors.New("model cost configuration is missing")
	ErrUsageUnknown             = errors.New("usage is unknown")
	ErrTerminalConflict         = errors.New("reservation has a different terminal result")
	ErrOutcomeUnknown           = errors.New("quota operation outcome is unknown")
	ErrInvariant                = errors.New("quota invariant violated")
)

type UsageSource string

const (
	UsageAuthoritative UsageSource = "authoritative"
	UsageEstimated     UsageSource = "estimated"
	UsageUnknown       UsageSource = "unknown"
)

type RequestStatus string

const (
	RequestQueued      RequestStatus = "queued"
	RequestDispatching RequestStatus = "dispatching"
	RequestStreaming   RequestStatus = "streaming"
	RequestCompleted   RequestStatus = "completed"
	RequestFailed      RequestStatus = "failed"
	RequestCanceled    RequestStatus = "canceled"
	RequestUncertain   RequestStatus = "uncertain"
)

type ReservationState string

const (
	ReservationReserved    ReservationState = "reserved"
	ReservationSettled     ReservationState = "settled"
	ReservationReleased    ReservationState = "released"
	ReservationCompensated ReservationState = "compensated"
)

type LedgerKind string

const (
	LedgerGrant        LedgerKind = "grant"
	LedgerReservation  LedgerKind = "reservation"
	LedgerSettlement   LedgerKind = "settlement"
	LedgerRelease      LedgerKind = "release"
	LedgerCompensation LedgerKind = "compensation"
	LedgerAdjustment   LedgerKind = "adjustment"
)

type LedgerEvent struct {
	ID              uuid.UUID   `json:"id"`
	UserID          uuid.UUID   `json:"user_id"`
	SubscriptionID  uuid.UUID   `json:"subscription_id"`
	ServicePlanName string      `json:"service_plan_name"`
	RequestID       *uuid.UUID  `json:"request_id,omitempty"`
	ReservationID   *uuid.UUID  `json:"reservation_id,omitempty"`
	Kind            LedgerKind  `json:"kind"`
	TokenDelta      int64       `json:"token_delta"`
	ReservedTokens  int64       `json:"reserved_tokens"`
	InputTokens     int64       `json:"input_tokens"`
	OutputTokens    int64       `json:"output_tokens"`
	UsageSource     UsageSource `json:"usage_source"`
	Note            *string     `json:"note,omitempty"`
	CreatedBy       *uuid.UUID  `json:"created_by,omitempty"`
	CreatedAt       time.Time   `json:"created_at"`
	OwnerName       string      `json:"-"`
	ActorName       *string     `json:"-"`
}

type Page struct {
	Offset int32
	Size   int32
}

type PageResult[T any] struct {
	Items []T   `json:"items"`
	Total int64 `json:"total"`
}

type LedgerFilter struct {
	UserID         *uuid.UUID
	SubscriptionID *uuid.UUID
	Search         string
	Page           Page
}

type RequestLogQuery struct {
	UserID         *uuid.UUID
	GatewayKeyID   *uuid.UUID
	ModelID        *uuid.UUID
	ResourcePoolID *uuid.UUID
	Search         string
	Status         RequestStatus
	From           time.Time
	To             time.Time
	Page           Page
}

type RequestLog struct {
	RequestID         uuid.UUID
	UserID            uuid.UUID
	UserName          string
	GatewayKeyID      uuid.UUID
	KeyPrefix         string
	ModelID           uuid.UUID
	ModelAlias        string
	ResourcePoolID    uuid.UUID
	ResourcePoolName  string
	ResourcePoolSlug  string
	Status            RequestStatus
	Stream            bool
	InputTokens       *int64
	OutputTokens      *int64
	UsageSource       UsageSource
	ErrorKind         *string
	AcceptedAt        time.Time
	CompletedAt       *time.Time
	UpdatedAt         time.Time
	AttemptCount      int64
	LastAttemptStatus *string
}

type RequestAttempt struct {
	ID                uuid.UUID
	Sequence          int32
	Status            string
	ProviderName      string
	CredentialName    string
	UpstreamRequestID *string
	HTTPStatus        *int32
	ErrorKind         *string
	RetryAfterAt      *time.Time
	SentAt            *time.Time
	FirstByteAt       *time.Time
	CompletedAt       *time.Time
	InputTokens       *int64
	OutputTokens      *int64
	UsageSource       UsageSource
	CreatedAt         time.Time
}

type RequestLogDetail struct {
	RequestLog
	Attempts []RequestAttempt
}

type AcceptInput struct {
	RequestID      uuid.UUID
	UserID         uuid.UUID
	GatewayKeyID   uuid.UUID
	ModelID        uuid.UUID
	Stream         bool
	RequestDigest  []byte
	IdempotencyKey *string
	ReservedTokens int64
}

type Request struct {
	ID                        uuid.UUID     `json:"id"`
	IdempotencyKey            *string       `json:"idempotency_key,omitempty"`
	UserID                    uuid.UUID     `json:"user_id"`
	GatewayKeyID              uuid.UUID     `json:"gateway_key_id"`
	ModelID                   uuid.UUID     `json:"model_id"`
	SubscriptionID            uuid.UUID     `json:"subscription_id"`
	ResourcePoolID            uuid.UUID     `json:"resource_pool_id"`
	PriceVersionID            uuid.UUID     `json:"price_version_id"`
	CostCurrency              string        `json:"cost_currency"`
	InputRateNanosPerMillion  int64         `json:"input_rate_nanos_per_million"`
	OutputRateNanosPerMillion int64         `json:"output_rate_nanos_per_million"`
	InputCostNanos            *int64        `json:"input_cost_nanos,omitempty"`
	OutputCostNanos           *int64        `json:"output_cost_nanos,omitempty"`
	TotalCostNanos            *int64        `json:"total_cost_nanos,omitempty"`
	Status                    RequestStatus `json:"status"`
	Stream                    bool          `json:"stream"`
	InputTokens               *int64        `json:"input_tokens,omitempty"`
	OutputTokens              *int64        `json:"output_tokens,omitempty"`
	UsageSource               UsageSource   `json:"usage_source"`
	ErrorKind                 *string       `json:"error_kind,omitempty"`
	ErrorDetail               *string       `json:"error_detail,omitempty"`
	AcceptedAt                time.Time     `json:"accepted_at"`
	CompletedAt               *time.Time    `json:"completed_at,omitempty"`
	UpdatedAt                 time.Time     `json:"updated_at"`
}

type Reservation struct {
	ID              uuid.UUID        `json:"id"`
	SubscriptionID  uuid.UUID        `json:"subscription_id"`
	RequestID       uuid.UUID        `json:"request_id"`
	State           ReservationState `json:"state"`
	ReservedTokens  int64            `json:"reserved_tokens"`
	ChargedTokens   int64            `json:"charged_tokens"`
	UsageSource     UsageSource      `json:"usage_source"`
	ReserveEventID  uuid.UUID        `json:"reserve_event_id"`
	TerminalEventID *uuid.UUID       `json:"terminal_event_id,omitempty"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
}

type SubscriptionCapacity struct {
	ID               uuid.UUID `json:"id"`
	ConcurrencyLimit int32     `json:"concurrency_limit"`
	RPMLimit         *int32    `json:"rpm_limit,omitempty"`
	TPMLimit         *int64    `json:"tpm_limit,omitempty"`
}

type AcceptedRequest struct {
	Request              Request              `json:"request"`
	Reservation          Reservation          `json:"reservation"`
	SubscriptionCapacity SubscriptionCapacity `json:"subscription_capacity"`
	Replayed             bool                 `json:"replayed"`
}

type Resolution struct {
	Request     Request     `json:"request"`
	Reservation Reservation `json:"reservation"`
}

type Repository interface {
	ListLedger(context.Context, LedgerFilter) (PageResult[LedgerEvent], error)
	ListRequestLogs(context.Context, RequestLogQuery) (PageResult[RequestLog], error)
	GetRequestLog(context.Context, uuid.UUID, *uuid.UUID) (RequestLogDetail, error)
	AcceptRequest(context.Context, AcceptInput) (AcceptedRequest, error)
	Settle(context.Context, uuid.UUID, execution.Claim, int64, int64, UsageSource) (Resolution, error)
	Release(context.Context, uuid.UUID, execution.Claim, string, string) (Resolution, error)
	ReleaseAccepted(context.Context, uuid.UUID, string, string) (Resolution, error)
	Compensate(context.Context, uuid.UUID, execution.Claim, int64, int64, UsageSource, string, string) (Resolution, error)
}

type Service struct {
	repository Repository
}

func NewService(repository Repository) (*Service, error) {
	if repository == nil {
		return nil, ErrInvalidInput
	}
	return &Service{repository: repository}, nil
}

func canReadUser(actor identity.Principal, userID uuid.UUID) bool {
	return actor.Status == identity.StatusActive && (actor.CanManageUsers() || actor.UserID == userID)
}
