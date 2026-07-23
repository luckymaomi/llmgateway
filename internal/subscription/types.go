package subscription

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/identity"
)

var (
	ErrInvalidInput        = errors.New("invalid subscription input")
	ErrNotFound            = errors.New("subscription record not found")
	ErrConflict            = errors.New("subscription conflict")
	ErrForbidden           = errors.New("subscription operation forbidden")
	ErrIdempotencyConflict = errors.New("subscription idempotency key conflict")
	ErrOutcomeUnknown      = errors.New("subscription operation outcome is unknown")
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

type PlanKind string

const (
	PlanToken  PlanKind = "token"
	PlanCoding PlanKind = "coding"
)

type PlanStatus string

const (
	PlanActive   PlanStatus = "active"
	PlanDisabled PlanStatus = "disabled"
	PlanArchived PlanStatus = "archived"
)

type PlanRoute struct {
	ModelID          uuid.UUID `json:"model_id"`
	ModelName        string    `json:"model_name,omitempty"`
	ResourcePoolID   uuid.UUID `json:"resource_pool_id"`
	ResourcePoolName string    `json:"resource_pool_name,omitempty"`
	ResourcePoolSlug string    `json:"resource_pool_slug,omitempty"`
	ProviderName     string    `json:"provider_name,omitempty"`
}

type PlanVersion struct {
	ID               uuid.UUID   `json:"id"`
	Version          int32       `json:"version"`
	TokenQuota       int64       `json:"token_quota"`
	ValidityDays     int32       `json:"validity_days"`
	ConcurrencyLimit int32       `json:"concurrency_limit"`
	RPMLimit         *int32      `json:"rpm_limit,omitempty"`
	TPMLimit         *int64      `json:"tpm_limit,omitempty"`
	Routes           []PlanRoute `json:"routes"`
	CreatedAt        time.Time   `json:"created_at"`
}

type ServicePlan struct {
	ID                      uuid.UUID    `json:"id"`
	Slug                    string       `json:"slug"`
	Name                    string       `json:"name"`
	Description             string       `json:"description"`
	Kind                    PlanKind     `json:"kind"`
	Status                  PlanStatus   `json:"status"`
	CurrentVersion          *PlanVersion `json:"current_version,omitempty"`
	ActiveSubscriptionCount int64        `json:"active_subscription_count"`
	CreatedAt               time.Time    `json:"created_at"`
	UpdatedAt               time.Time    `json:"updated_at"`
}

type PlanDraft struct {
	ID               uuid.UUID
	Slug             string
	Name             string
	Description      string
	Kind             PlanKind
	TokenQuota       int64
	ValidityDays     int32
	ConcurrencyLimit int32
	RPMLimit         *int32
	TPMLimit         *int64
	Routes           []PlanRoute
}

type SubscriptionStatus string

const (
	StatusScheduled SubscriptionStatus = "scheduled"
	StatusActive    SubscriptionStatus = "active"
	StatusSuspended SubscriptionStatus = "suspended"
	StatusCanceled  SubscriptionStatus = "canceled"
	StatusExpired   SubscriptionStatus = "expired"
)

type Subscription struct {
	ID                   uuid.UUID          `json:"id"`
	UserID               uuid.UUID          `json:"user_id"`
	MemberEmail          string             `json:"member_email"`
	MemberName           string             `json:"member_name"`
	ServicePlanID        uuid.UUID          `json:"service_plan_id"`
	ServicePlanVersionID uuid.UUID          `json:"service_plan_version_id"`
	ServicePlanName      string             `json:"service_plan_name"`
	PlanKind             PlanKind           `json:"plan_kind"`
	PlanVersion          int32              `json:"plan_version"`
	Status               SubscriptionStatus `json:"status"`
	GrantedTokens        int64              `json:"granted_tokens"`
	BalanceTokens        int64              `json:"balance_tokens"`
	StartsAt             time.Time          `json:"starts_at"`
	ExpiresAt            time.Time          `json:"expires_at"`
	Notes                string             `json:"notes"`
	ConcurrencyLimit     int32              `json:"concurrency_limit"`
	RPMLimit             *int32             `json:"rpm_limit,omitempty"`
	TPMLimit             *int64             `json:"tpm_limit,omitempty"`
	Routes               []PlanRoute        `json:"routes"`
	SuspendedAt          *time.Time         `json:"suspended_at,omitempty"`
	CanceledAt           *time.Time         `json:"canceled_at,omitempty"`
	CreatedAt            time.Time          `json:"created_at"`
	UpdatedAt            time.Time          `json:"updated_at"`
}

type NewSubscription struct {
	UserID        uuid.UUID
	ServicePlanID uuid.UUID
	GrantedTokens int64
	StartsAt      time.Time
	ExpiresAt     time.Time
	Notes         string
}

type SubscriptionChange struct {
	ID                uuid.UUID
	GrantedTokens     int64
	StartsAt          time.Time
	ExpiresAt         time.Time
	Notes             string
	ExpectedUpdatedAt time.Time
}

type Query struct {
	Actor  identity.Principal
	UserID *uuid.UUID
	Search string
	Status string
	Offset int32
	Size   int32
}

type Page struct {
	Items []Subscription `json:"items"`
	Total int64          `json:"total"`
}

type Repository interface {
	PublishPlan(context.Context, PlanDraft, uuid.UUID, Mutation) (ServicePlan, error)
	SetPlanStatus(context.Context, uuid.UUID, PlanStatus, uuid.UUID, Mutation) (ServicePlan, error)
	ListPlans(context.Context, bool) ([]ServicePlan, error)
	GetPlan(context.Context, uuid.UUID) (ServicePlan, error)

	CreateSubscription(context.Context, NewSubscription, uuid.UUID, Mutation) (Subscription, error)
	UpdateSubscription(context.Context, SubscriptionChange, SubscriptionStatus, uuid.UUID, Mutation) (Subscription, error)
	SetSubscriptionStatus(context.Context, uuid.UUID, SubscriptionStatus, time.Time, uuid.UUID, Mutation) (Subscription, error)
	ListSubscriptions(context.Context, Query) (Page, error)
	GetSubscription(context.Context, uuid.UUID) (Subscription, error)
}
