package costing

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/identity"
)

var (
	ErrInvalidInput         = errors.New("invalid costing input")
	ErrForbidden            = errors.New("costing operation forbidden")
	ErrNotFound             = errors.New("costing record not found")
	ErrConflict             = errors.New("costing conflict")
	ErrOutcomeUnknown       = errors.New("costing operation outcome is unknown")
	ErrConfigurationMissing = errors.New("model cost configuration is missing")
	ErrOverflow             = errors.New("cost amount exceeds supported range")
)

const MaximumRateNanosPerMillion int64 = 1_000_000_000_000_000

type MutationRequest struct {
	IdempotencyKey uuid.UUID
	RequestID      string
}

type NewPriceVersion struct {
	ModelID                     uuid.UUID
	Currency                    string
	InputPricePerMillionTokens  string
	OutputPricePerMillionTokens string
	EffectiveAt                 time.Time
}

type PriceVersion struct {
	ID                        uuid.UUID
	ModelID                   uuid.UUID
	ModelAlias                string
	Currency                  string
	InputRateNanosPerMillion  int64
	OutputRateNanosPerMillion int64
	EffectiveAt               time.Time
	CreatedBy                 uuid.UUID
	CreatedAt                 time.Time
	Replayed                  bool
}

type Summary struct {
	UserID           uuid.UUID
	UserName         string
	SubscriptionID   uuid.UUID
	ServicePlanName  string
	PlanKind         string
	ModelID          uuid.UUID
	ModelAlias       string
	ProviderID       uuid.UUID
	ProviderName     string
	ResourcePoolID   uuid.UUID
	ResourcePoolName string
	Currency         string
	RequestCount     int64
	InputTokens      int64
	OutputTokens     int64
	InputCostNanos   int64
	OutputCostNanos  int64
	TotalCostNanos   int64
}

type Page struct {
	Offset int32
	Size   int32
}

type Repository interface {
	CreatePriceVersion(context.Context, NewPriceVersion, MutationRequest, uuid.UUID) (PriceVersion, error)
	ListPriceVersions(context.Context, *uuid.UUID, Page) ([]PriceVersion, error)
	ListSummaries(context.Context, Page) ([]Summary, error)
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

func canManageCosts(actor identity.Principal) bool {
	return actor.Status == identity.StatusActive && actor.CanManageUsers()
}
