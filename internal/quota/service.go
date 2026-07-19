package quota

import (
	"context"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/identity"
)

func (s *Service) AuthorizeModel(ctx context.Context, actor identity.Principal, userID, modelID uuid.UUID) error {
	if actor.Status != identity.StatusActive || !actor.CanManageUsers() {
		return ErrForbidden
	}
	if userID == uuid.Nil || modelID == uuid.Nil {
		return ErrInvalidInput
	}
	return s.repository.AuthorizeModel(ctx, userID, modelID, actor.UserID)
}

func (s *Service) RevokeModel(ctx context.Context, actor identity.Principal, userID, modelID uuid.UUID) error {
	if actor.Status != identity.StatusActive || !actor.CanManageUsers() {
		return ErrForbidden
	}
	if userID == uuid.Nil || modelID == uuid.Nil {
		return ErrInvalidInput
	}
	return s.repository.RevokeModel(ctx, userID, modelID, actor.UserID)
}

func (s *Service) ListModelAuthorizations(ctx context.Context, actor identity.Principal, userID uuid.UUID) ([]ModelAuthorization, error) {
	if userID == uuid.Nil {
		return nil, ErrInvalidInput
	}
	if !canReadUser(actor, userID) {
		return nil, ErrForbidden
	}
	return s.repository.ListModelAuthorizations(ctx, userID)
}

func (s *Service) CreateEntitlement(ctx context.Context, actor identity.Principal, input NewEntitlement) (Entitlement, error) {
	if actor.Status != identity.StatusActive || !actor.CanManageUsers() {
		return Entitlement{}, ErrForbidden
	}
	input.Note = strings.TrimSpace(input.Note)
	input.StartsAt = input.StartsAt.UTC()
	input.ExpiresAt = input.ExpiresAt.UTC()
	if err := validateEntitlement(input); err != nil {
		return Entitlement{}, err
	}
	return s.repository.CreateEntitlement(ctx, input, actor.UserID)
}

func (s *Service) ListEntitlements(ctx context.Context, actor identity.Principal, userID *uuid.UUID, page Page) ([]Entitlement, error) {
	page = normalizePage(page)
	if actor.Status != identity.StatusActive {
		return nil, ErrForbidden
	}
	if actor.CanManageUsers() {
		return s.repository.ListEntitlements(ctx, userID, page)
	}
	if userID != nil && *userID != actor.UserID {
		return nil, ErrForbidden
	}
	ownUserID := actor.UserID
	return s.repository.ListEntitlements(ctx, &ownUserID, page)
}

func (s *Service) ListLedger(ctx context.Context, actor identity.Principal, filter LedgerFilter) ([]LedgerEvent, error) {
	filter.Page = normalizePage(filter.Page)
	if actor.Status != identity.StatusActive {
		return nil, ErrForbidden
	}
	if !actor.CanManageUsers() {
		if filter.UserID != nil && *filter.UserID != actor.UserID {
			return nil, ErrForbidden
		}
		ownUserID := actor.UserID
		filter.UserID = &ownUserID
	}
	return s.repository.ListLedger(ctx, filter)
}

func (s *Service) AcceptRequest(ctx context.Context, input AcceptInput) (AcceptedRequest, error) {
	if input.UserID == uuid.Nil || input.GatewayKeyID == uuid.Nil || input.ModelID == uuid.Nil || input.ReservedTokens < 1 || len(input.RequestDigest) != 32 || !validDomain(input.ResourceDomain) {
		return AcceptedRequest{}, ErrInvalidInput
	}
	if input.ConfigRevisionID != nil && *input.ConfigRevisionID == uuid.Nil {
		return AcceptedRequest{}, ErrInvalidInput
	}
	if input.IdempotencyKey != nil {
		value := strings.TrimSpace(*input.IdempotencyKey)
		if value == "" || utf8.RuneCountInString(value) > 200 {
			return AcceptedRequest{}, ErrInvalidInput
		}
		input.IdempotencyKey = &value
	}
	input.RequestDigest = append([]byte(nil), input.RequestDigest...)
	return s.repository.AcceptRequest(ctx, input)
}

func (s *Service) Settle(ctx context.Context, requestID uuid.UUID, inputTokens, outputTokens int64, source UsageSource) (Resolution, error) {
	if source == UsageUnknown && inputTokens == 0 && outputTokens == 0 {
		return Resolution{}, ErrUsageUnknown
	}
	if requestID == uuid.Nil || !validKnownUsage(inputTokens, outputTokens, source) {
		return Resolution{}, ErrInvalidInput
	}
	return s.repository.Settle(ctx, requestID, inputTokens, outputTokens, source)
}

func (s *Service) Release(ctx context.Context, requestID uuid.UUID, errorKind, errorDetail string) (Resolution, error) {
	errorKind, errorDetail = normalizeFailure(errorKind, errorDetail)
	if requestID == uuid.Nil || errorKind == "" {
		return Resolution{}, ErrInvalidInput
	}
	return s.repository.Release(ctx, requestID, errorKind, errorDetail)
}

func (s *Service) Compensate(ctx context.Context, requestID uuid.UUID, inputTokens, outputTokens int64, source UsageSource, errorKind, errorDetail string) (Resolution, error) {
	errorKind, errorDetail = normalizeFailure(errorKind, errorDetail)
	if source == UsageUnknown && inputTokens == 0 && outputTokens == 0 {
		return Resolution{}, ErrUsageUnknown
	}
	if requestID == uuid.Nil || errorKind == "" || !validKnownUsage(inputTokens, outputTokens, source) {
		return Resolution{}, ErrInvalidInput
	}
	return s.repository.Compensate(ctx, requestID, inputTokens, outputTokens, source, errorKind, errorDetail)
}

func validateEntitlement(input NewEntitlement) error {
	if input.IdempotencyKey == uuid.Nil || input.UserID == uuid.Nil || input.GrantedTokens < 1 || input.ConcurrencyLimit < 1 || input.ConcurrencyLimit > 10000 || input.StartsAt.IsZero() || input.ExpiresAt.IsZero() || !input.ExpiresAt.After(input.StartsAt) || input.Note == "" || utf8.RuneCountInString(input.Note) > 500 {
		return ErrInvalidInput
	}
	if input.Plan != PlanToken && input.Plan != PlanCoding || !validDomain(input.ResourceDomain) {
		return ErrInvalidInput
	}
	if input.ModelID != nil && *input.ModelID == uuid.Nil || input.RPMLimit != nil && *input.RPMLimit < 1 || input.TPMLimit != nil && *input.TPMLimit < 1 {
		return ErrInvalidInput
	}
	return nil
}

func validDomain(domain ResourceDomain) bool {
	return domain == ResourceFree || domain == ResourceProfessional
}

func validKnownUsage(inputTokens, outputTokens int64, source UsageSource) bool {
	if inputTokens < 0 || outputTokens < 0 || inputTokens > math.MaxInt64-outputTokens {
		return false
	}
	return source == UsageAuthoritative || source == UsageEstimated
}

func normalizeFailure(kind, detail string) (string, string) {
	kind = strings.TrimSpace(kind)
	detail = strings.TrimSpace(detail)
	if utf8.RuneCountInString(kind) > 100 || utf8.RuneCountInString(detail) > 2000 {
		return "", ""
	}
	return kind, detail
}

func normalizePage(page Page) Page {
	if page.Offset < 0 {
		page.Offset = 0
	}
	if page.Size < 1 {
		page.Size = 50
	}
	if page.Size > 200 {
		page.Size = 200
	}
	return page
}
