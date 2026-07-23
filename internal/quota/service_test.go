package quota

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/execution"
	"github.com/luckymaomi/llmgateway/internal/identity"
)

type repositoryStub struct {
	ledgerUserID  *uuid.UUID
	requestUserID *uuid.UUID
	detailUserID  *uuid.UUID
	accepted      *AcceptInput
	settleCalls   int
	releaseKind   string
}

func (r *repositoryStub) ListLedger(_ context.Context, filter LedgerFilter) (PageResult[LedgerEvent], error) {
	r.ledgerUserID = filter.UserID
	return PageResult[LedgerEvent]{Items: []LedgerEvent{}}, nil
}

func (r *repositoryStub) ListRequestLogs(_ context.Context, query RequestLogQuery) (PageResult[RequestLog], error) {
	r.requestUserID = query.UserID
	return PageResult[RequestLog]{Items: []RequestLog{}}, nil
}

func (r *repositoryStub) GetRequestLog(_ context.Context, _ uuid.UUID, userID *uuid.UUID) (RequestLogDetail, error) {
	r.detailUserID = userID
	return RequestLogDetail{}, nil
}

func (r *repositoryStub) AcceptRequest(_ context.Context, input AcceptInput) (AcceptedRequest, error) {
	r.accepted = &input
	return AcceptedRequest{}, nil
}

func (r *repositoryStub) Settle(context.Context, uuid.UUID, execution.Claim, int64, int64, UsageSource) (Resolution, error) {
	r.settleCalls++
	return Resolution{}, nil
}

func (r *repositoryStub) Release(_ context.Context, _ uuid.UUID, _ execution.Claim, kind, _ string) (Resolution, error) {
	r.releaseKind = kind
	return Resolution{}, nil
}

func (r *repositoryStub) ReleaseAccepted(_ context.Context, _ uuid.UUID, kind, _ string) (Resolution, error) {
	r.releaseKind = kind
	return Resolution{}, nil
}

func (r *repositoryStub) Compensate(context.Context, uuid.UUID, execution.Claim, int64, int64, UsageSource, string, string) (Resolution, error) {
	return Resolution{}, nil
}

func activePrincipal(role identity.Role, userID uuid.UUID) identity.Principal {
	return identity.Principal{UserID: userID, Role: role, Status: identity.StatusActive}
}

func TestMemberQuotaReadsAreScopedToTheAuthenticatedOwner(t *testing.T) {
	repository := &repositoryStub{}
	service, _ := NewService(repository)
	memberID := uuid.New()
	now := time.Now().UTC()
	if _, err := service.ListLedger(context.Background(), activePrincipal(identity.RoleMember, memberID), LedgerFilter{}); err != nil {
		t.Fatalf("ListLedger() error = %v", err)
	}
	if repository.ledgerUserID == nil || *repository.ledgerUserID != memberID {
		t.Fatalf("ledger repository user filter = %v, want %s", repository.ledgerUserID, memberID)
	}
	if _, err := service.ListRequestLogs(context.Background(), activePrincipal(identity.RoleMember, memberID), RequestLogQuery{From: now.Add(-time.Hour), To: now}); err != nil {
		t.Fatalf("ListRequestLogs() error = %v", err)
	}
	if repository.requestUserID == nil || *repository.requestUserID != memberID {
		t.Fatalf("request repository user filter = %v, want %s", repository.requestUserID, memberID)
	}
	if _, err := service.GetRequestLog(context.Background(), activePrincipal(identity.RoleMember, memberID), uuid.New()); err != nil {
		t.Fatalf("GetRequestLog() error = %v", err)
	}
	if repository.detailUserID == nil || *repository.detailUserID != memberID {
		t.Fatalf("request detail repository user filter = %v, want %s", repository.detailUserID, memberID)
	}
}

func TestAcceptRequestPassesAStableDigestAndNormalizedIdempotencyKey(t *testing.T) {
	repository := &repositoryStub{}
	service, _ := NewService(repository)
	digest := make([]byte, 32)
	key := "  retry-42  "
	_, err := service.AcceptRequest(context.Background(), AcceptInput{
		RequestID: uuid.New(), UserID: uuid.New(), GatewayKeyID: uuid.New(), ModelID: uuid.New(),
		RequestDigest: digest, IdempotencyKey: &key, ReservedTokens: 512,
	})
	if err != nil {
		t.Fatalf("AcceptRequest() error = %v", err)
	}
	digest[0] = 1
	if repository.accepted == nil || *repository.accepted.IdempotencyKey != "retry-42" || repository.accepted.RequestDigest[0] != 0 {
		t.Fatalf("accepted input = %#v", repository.accepted)
	}
}

func TestAcceptRequestRejectsIncompleteIdentityBeforePersistence(t *testing.T) {
	repository := &repositoryStub{}
	service, _ := NewService(repository)
	_, err := service.AcceptRequest(context.Background(), AcceptInput{
		UserID: uuid.New(), GatewayKeyID: uuid.New(), ModelID: uuid.New(), RequestDigest: make([]byte, 32), ReservedTokens: 512,
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("AcceptRequest() error = %v, want ErrInvalidInput", err)
	}
	if repository.accepted != nil {
		t.Fatal("request without a stable request ID reached persistence")
	}
}

func TestUnknownUsageKeepsTheReservationForRecovery(t *testing.T) {
	repository := &repositoryStub{}
	service, _ := NewService(repository)
	requestID := uuid.New()
	_, err := service.Settle(context.Background(), requestID, execution.Claim{RequestID: requestID, ExecutionID: uuid.New(), Generation: 1}, 0, 0, UsageUnknown)
	if !errors.Is(err, ErrUsageUnknown) {
		t.Fatalf("Settle() error = %v, want ErrUsageUnknown", err)
	}
	if repository.settleCalls != 0 {
		t.Fatalf("repository settle calls = %d, want 0", repository.settleCalls)
	}
}
