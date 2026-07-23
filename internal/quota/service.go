package quota

import (
	"context"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/execution"
	"github.com/luckymaomi/llmgateway/internal/identity"
)

func (s *Service) ListLedger(ctx context.Context, actor identity.Principal, filter LedgerFilter) (PageResult[LedgerEvent], error) {
	filter.Page = normalizePage(filter.Page)
	filter.Search = strings.TrimSpace(filter.Search)
	if actor.Status != identity.StatusActive || len(filter.Search) > 200 {
		return PageResult[LedgerEvent]{}, ErrForbidden
	}
	if !actor.CanManageUsers() {
		if filter.UserID != nil && *filter.UserID != actor.UserID {
			return PageResult[LedgerEvent]{}, ErrForbidden
		}
		filter.UserID = &actor.UserID
	}
	return s.repository.ListLedger(ctx, filter)
}

func (s *Service) ListRequestLogs(ctx context.Context, actor identity.Principal, query RequestLogQuery) (PageResult[RequestLog], error) {
	query.Page = normalizePage(query.Page)
	query.Search = strings.TrimSpace(query.Search)
	if actor.Status != identity.StatusActive || len(query.Search) > 200 || query.Status != "" && !validRequestStatus(query.Status) || query.From.IsZero() || query.To.IsZero() || !query.To.After(query.From) || query.To.Sub(query.From) > 31*24*time.Hour {
		return PageResult[RequestLog]{}, ErrInvalidInput
	}
	query.From, query.To = query.From.UTC(), query.To.UTC()
	if actor.CanManageUsers() {
		return s.repository.ListRequestLogs(ctx, query)
	}
	if actor.Role != identity.RoleMember || query.UserID != nil && *query.UserID != actor.UserID {
		return PageResult[RequestLog]{}, ErrForbidden
	}
	query.UserID = &actor.UserID
	return s.repository.ListRequestLogs(ctx, query)
}

func (s *Service) GetRequestLog(ctx context.Context, actor identity.Principal, requestID uuid.UUID) (RequestLogDetail, error) {
	if actor.Status != identity.StatusActive || requestID == uuid.Nil {
		return RequestLogDetail{}, ErrForbidden
	}
	var userID *uuid.UUID
	if !actor.CanManageUsers() {
		if actor.Role != identity.RoleMember {
			return RequestLogDetail{}, ErrForbidden
		}
		userID = &actor.UserID
	}
	return s.repository.GetRequestLog(ctx, requestID, userID)
}

func (s *Service) AcceptRequest(ctx context.Context, input AcceptInput) (AcceptedRequest, error) {
	if input.RequestID == uuid.Nil || input.UserID == uuid.Nil || input.GatewayKeyID == uuid.Nil || input.ModelID == uuid.Nil || input.ReservedTokens < 1 || len(input.RequestDigest) != 32 {
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

func validRequestStatus(status RequestStatus) bool {
	return status == RequestQueued || status == RequestDispatching || status == RequestStreaming || status == RequestCompleted || status == RequestFailed || status == RequestCanceled || status == RequestUncertain
}

func validKnownUsage(inputTokens, outputTokens int64, source UsageSource) bool {
	return inputTokens >= 0 && outputTokens >= 0 && inputTokens <= math.MaxInt64-outputTokens && (source == UsageAuthoritative || source == UsageEstimated)
}

func normalizeFailure(kind, detail string) (string, string) {
	kind, detail = strings.TrimSpace(kind), strings.TrimSpace(detail)
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
