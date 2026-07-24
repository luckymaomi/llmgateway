package subscription

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/identity"
)

type Service struct {
	repository Repository
	now        func() time.Time
}

func NewService(repository Repository) (*Service, error) {
	if repository == nil {
		return nil, ErrInvalidInput
	}
	return &Service{repository: repository, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (s *Service) PublishPlan(ctx context.Context, actor identity.Principal, draft PlanDraft, request MutationRequest) (ServicePlan, error) {
	if !administrator(actor) {
		return ServicePlan{}, ErrForbidden
	}
	draft.Name, draft.Description = strings.TrimSpace(draft.Name), strings.TrimSpace(draft.Description)
	if request.IdempotencyKey == uuid.Nil || utf8.RuneCountInString(draft.Name) < 1 || utf8.RuneCountInString(draft.Name) > 100 || utf8.RuneCountInString(draft.Description) > 500 || draft.Kind != PlanToken && draft.Kind != PlanCoding || draft.TokenQuota < 1 || draft.ValidityDays < 1 || draft.ValidityDays > 3650 || draft.ConcurrencyLimit < 1 || !validOptionalLimits(draft.RPMLimit, draft.TPMLimit) || !validRoutes(draft.Routes) {
		return ServicePlan{}, ErrInvalidInput
	}
	action := "service_plan.create"
	if draft.ID == uuid.Nil {
		draft.Slug = "plan-" + strings.ReplaceAll(request.IdempotencyKey.String(), "-", "")
	} else {
		action = "service_plan.publish"
	}
	mutation, err := makeMutation(request, action, draft)
	if err != nil {
		return ServicePlan{}, err
	}
	return s.repository.PublishPlan(ctx, draft, actor.UserID, mutation)
}

func (s *Service) SetPlanStatus(ctx context.Context, actor identity.Principal, id uuid.UUID, status PlanStatus, request MutationRequest) (ServicePlan, error) {
	if !administrator(actor) {
		return ServicePlan{}, ErrForbidden
	}
	if id == uuid.Nil || status != PlanActive && status != PlanDisabled && status != PlanArchived {
		return ServicePlan{}, ErrInvalidInput
	}
	mutation, err := makeMutation(request, "service_plan.status", struct {
		ID     uuid.UUID  `json:"id"`
		Status PlanStatus `json:"status"`
	}{id, status})
	if err != nil {
		return ServicePlan{}, err
	}
	return s.repository.SetPlanStatus(ctx, id, status, actor.UserID, mutation)
}

func (s *Service) ListPlans(ctx context.Context, actor identity.Principal, includeArchived bool) ([]ServicePlan, error) {
	if actor.Status != identity.StatusActive {
		return nil, ErrForbidden
	}
	return s.repository.ListPlans(ctx, includeArchived && actor.Role == identity.RoleAdministrator)
}

func (s *Service) CreateSubscription(ctx context.Context, actor identity.Principal, input NewSubscription, request MutationRequest) (Subscription, error) {
	if !administrator(actor) {
		return Subscription{}, ErrForbidden
	}
	input.StartsAt, input.ExpiresAt, input.Notes = input.StartsAt.UTC(), input.ExpiresAt.UTC(), strings.TrimSpace(input.Notes)
	if input.UserID == uuid.Nil || input.ServicePlanID == uuid.Nil || input.GrantedTokens < 1 || input.StartsAt.IsZero() || !input.ExpiresAt.After(input.StartsAt) || utf8.RuneCountInString(input.Notes) > 500 {
		return Subscription{}, ErrInvalidInput
	}
	mutation, err := makeMutation(request, "subscription.create", input)
	if err != nil {
		return Subscription{}, err
	}
	return s.repository.CreateSubscription(ctx, input, actor.UserID, mutation)
}

func (s *Service) UpdateSubscription(ctx context.Context, actor identity.Principal, change SubscriptionChange, request MutationRequest) (Subscription, error) {
	if !administrator(actor) {
		return Subscription{}, ErrForbidden
	}
	change.StartsAt, change.ExpiresAt, change.ExpectedUpdatedAt, change.Notes = change.StartsAt.UTC(), change.ExpiresAt.UTC(), change.ExpectedUpdatedAt.UTC(), strings.TrimSpace(change.Notes)
	if change.ID == uuid.Nil || change.GrantedTokens < 1 || change.ExpectedUpdatedAt.IsZero() || !change.ExpiresAt.After(change.StartsAt) || utf8.RuneCountInString(change.Notes) > 500 {
		return Subscription{}, ErrInvalidInput
	}
	status := StatusActive
	if change.StartsAt.After(s.now()) {
		status = StatusScheduled
	} else if !change.ExpiresAt.After(s.now()) {
		status = StatusExpired
	}
	mutation, err := makeMutation(request, "subscription.update", change)
	if err != nil {
		return Subscription{}, err
	}
	return s.repository.UpdateSubscription(ctx, change, status, actor.UserID, mutation)
}

func (s *Service) SetSubscriptionStatus(ctx context.Context, actor identity.Principal, id uuid.UUID, status SubscriptionStatus, expectedUpdatedAt time.Time, request MutationRequest) (Subscription, error) {
	if !administrator(actor) {
		return Subscription{}, ErrForbidden
	}
	if id == uuid.Nil || expectedUpdatedAt.IsZero() || status != StatusActive && status != StatusSuspended && status != StatusCanceled {
		return Subscription{}, ErrInvalidInput
	}
	mutation, err := makeMutation(request, "subscription.status", struct {
		ID                uuid.UUID          `json:"id"`
		Status            SubscriptionStatus `json:"status"`
		ExpectedUpdatedAt time.Time          `json:"expected_updated_at"`
	}{id, status, expectedUpdatedAt.UTC().Truncate(time.Microsecond)})
	if err != nil {
		return Subscription{}, err
	}
	return s.repository.SetSubscriptionStatus(ctx, id, status, expectedUpdatedAt.UTC(), actor.UserID, mutation)
}

func (s *Service) ListSubscriptions(ctx context.Context, actor identity.Principal, query Query) (Page, error) {
	if actor.Status != identity.StatusActive {
		return Page{}, ErrForbidden
	}
	query.Actor, query.Search = actor, strings.TrimSpace(query.Search)
	if actor.Role == identity.RoleMember {
		query.UserID = &actor.UserID
	} else if actor.Role != identity.RoleAdministrator {
		return Page{}, ErrForbidden
	}
	if query.Size < 1 || query.Size > 200 {
		query.Size = 50
	}
	if query.Offset < 0 || utf8.RuneCountInString(query.Search) > 200 {
		return Page{}, ErrInvalidInput
	}
	return s.repository.ListSubscriptions(ctx, query)
}

func makeMutation(request MutationRequest, action string, payload any) (Mutation, error) {
	if request.IdempotencyKey == uuid.Nil || strings.TrimSpace(request.RequestID) == "" || len(request.RequestID) > 128 {
		return Mutation{}, ErrInvalidInput
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return Mutation{}, ErrInvalidInput
	}
	digest := sha256.Sum256(encoded)
	return Mutation{Action: action, IdempotencyKey: request.IdempotencyKey, RequestFingerprint: digest[:], RequestID: request.RequestID}, nil
}

func administrator(actor identity.Principal) bool {
	return actor.Status == identity.StatusActive && actor.Role == identity.RoleAdministrator
}

func validOptionalLimits(rpm *int32, tpm *int64) bool {
	return (rpm == nil || *rpm > 0) && (tpm == nil || *tpm > 0)
}

func validRoutes(routes []PlanRoute) bool {
	if len(routes) == 0 || len(routes) > 100 {
		return false
	}
	seen := make(map[uuid.UUID]struct{}, len(routes))
	for _, route := range routes {
		if route.ModelID == uuid.Nil || route.ResourcePoolID == uuid.Nil {
			return false
		}
		if _, found := seen[route.ModelID]; found {
			return false
		}
		seen[route.ModelID] = struct{}{}
	}
	return true
}
