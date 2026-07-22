package quota

import (
	"context"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/execution"
	"github.com/luckymaomi/llmgateway/internal/identity"
)

func (s *Service) CreateEntitlement(ctx context.Context, actor identity.Principal, input NewEntitlement) (Entitlement, error) {
	if actor.Status != identity.StatusActive || !actor.CanManageUsers() {
		return Entitlement{}, ErrForbidden
	}
	input.Note = strings.TrimSpace(input.Note)
	input.RequestID = strings.TrimSpace(input.RequestID)
	input.StartsAt = input.StartsAt.UTC()
	input.ExpiresAt = input.ExpiresAt.UTC()
	if err := validateEntitlement(input); err != nil {
		return Entitlement{}, err
	}
	return s.repository.CreateEntitlement(ctx, input, actor.UserID)
}

func (s *Service) ListEntitlements(ctx context.Context, actor identity.Principal, query EntitlementQuery) (PageResult[Entitlement], error) {
	query.Page = normalizePage(query.Page)
	query.Search = strings.TrimSpace(query.Search)
	if actor.Status != identity.StatusActive {
		return PageResult[Entitlement]{}, ErrForbidden
	}
	if len(query.Search) > 200 || query.Status != "" && query.Status != "active" && query.Status != "scheduled" && query.Status != "expired" || query.ResourceDomain != "" && !validDomain(query.ResourceDomain) {
		return PageResult[Entitlement]{}, ErrInvalidInput
	}
	if actor.CanManageUsers() {
		return s.repository.ListEntitlements(ctx, query)
	}
	if query.UserID != nil && *query.UserID != actor.UserID {
		return PageResult[Entitlement]{}, ErrForbidden
	}
	query.UserID = &actor.UserID
	return s.repository.ListEntitlements(ctx, query)
}

func (s *Service) ListLedger(ctx context.Context, actor identity.Principal, filter LedgerFilter) (PageResult[LedgerEvent], error) {
	filter.Page = normalizePage(filter.Page)
	filter.Search = strings.TrimSpace(filter.Search)
	if actor.Status != identity.StatusActive {
		return PageResult[LedgerEvent]{}, ErrForbidden
	}
	if len(filter.Search) > 200 || filter.ResourceDomain != "" && !validDomain(filter.ResourceDomain) {
		return PageResult[LedgerEvent]{}, ErrInvalidInput
	}
	if !actor.CanManageUsers() {
		if filter.UserID != nil && *filter.UserID != actor.UserID {
			return PageResult[LedgerEvent]{}, ErrForbidden
		}
		filter.UserID = &actor.UserID
	}
	return s.repository.ListLedger(ctx, filter)
}

func (s *Service) ListUsage(ctx context.Context, actor identity.Principal, query UsageQuery) (PageResult[UsageRecord], error) {
	query.Page = normalizePage(query.Page)
	query.Search = strings.TrimSpace(query.Search)
	if actor.Status != identity.StatusActive {
		return PageResult[UsageRecord]{}, ErrForbidden
	}
	if len(query.Search) > 200 || query.ResourceDomain != "" && !validDomain(query.ResourceDomain) {
		return PageResult[UsageRecord]{}, ErrInvalidInput
	}
	if actor.CanManageUsers() {
		return s.repository.ListUsage(ctx, query)
	}
	if actor.Role != identity.RoleMember {
		return PageResult[UsageRecord]{}, ErrForbidden
	}
	if query.UserID != nil && *query.UserID != actor.UserID {
		return PageResult[UsageRecord]{}, ErrForbidden
	}
	query.UserID = &actor.UserID
	return s.repository.ListUsage(ctx, query)
}

func (s *Service) AcceptRequest(ctx context.Context, input AcceptInput) (AcceptedRequest, error) {
	if input.RequestID == uuid.Nil || input.UserID == uuid.Nil || input.GatewayKeyID == uuid.Nil || input.ModelID == uuid.Nil || input.ConfigRevisionID == nil || *input.ConfigRevisionID == uuid.Nil || input.ReservedTokens < 1 || len(input.RequestDigest) != 32 || !validDomain(input.ResourceDomain) {
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

func (s *Service) Settle(ctx context.Context, requestID uuid.UUID, claim execution.Claim, inputTokens, outputTokens int64, source UsageSource) (Resolution, error) {
	if source == UsageUnknown && inputTokens == 0 && outputTokens == 0 {
		return Resolution{}, ErrUsageUnknown
	}
	if requestID == uuid.Nil || claim.RequestID != requestID || !claim.Valid() || !validKnownUsage(inputTokens, outputTokens, source) {
		return Resolution{}, ErrInvalidInput
	}
	return s.repository.Settle(ctx, requestID, claim, inputTokens, outputTokens, source)
}

func (s *Service) Release(ctx context.Context, requestID uuid.UUID, claim execution.Claim, errorKind, errorDetail string) (Resolution, error) {
	errorKind, errorDetail = normalizeFailure(errorKind, errorDetail)
	if requestID == uuid.Nil || claim.RequestID != requestID || !claim.Valid() || errorKind == "" {
		return Resolution{}, ErrInvalidInput
	}
	return s.repository.Release(ctx, requestID, claim, errorKind, errorDetail)
}

func (s *Service) ReleaseAccepted(ctx context.Context, requestID uuid.UUID, errorKind, errorDetail string) (Resolution, error) {
	errorKind, errorDetail = normalizeFailure(errorKind, errorDetail)
	if requestID == uuid.Nil || errorKind == "" {
		return Resolution{}, ErrInvalidInput
	}
	return s.repository.ReleaseAccepted(ctx, requestID, errorKind, errorDetail)
}

func (s *Service) Compensate(ctx context.Context, requestID uuid.UUID, claim execution.Claim, inputTokens, outputTokens int64, source UsageSource, errorKind, errorDetail string) (Resolution, error) {
	errorKind, errorDetail = normalizeFailure(errorKind, errorDetail)
	if source == UsageUnknown && inputTokens == 0 && outputTokens == 0 {
		return Resolution{}, ErrUsageUnknown
	}
	if requestID == uuid.Nil || claim.RequestID != requestID || !claim.Valid() || errorKind == "" || !validKnownUsage(inputTokens, outputTokens, source) {
		return Resolution{}, ErrInvalidInput
	}
	return s.repository.Compensate(ctx, requestID, claim, inputTokens, outputTokens, source, errorKind, errorDetail)
}

func validateEntitlement(input NewEntitlement) error {
	if input.IdempotencyKey == uuid.Nil || input.RequestID == "" || utf8.RuneCountInString(input.RequestID) > 128 || input.UserID == uuid.Nil || input.GrantedTokens < 1 || input.ConcurrencyLimit < 1 || input.ConcurrencyLimit > 10000 || input.StartsAt.IsZero() || input.ExpiresAt.IsZero() || !input.ExpiresAt.After(input.StartsAt) || input.Note == "" || utf8.RuneCountInString(input.Note) > 500 {
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
