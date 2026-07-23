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
	created         *NewEntitlement
	listedUserID    *uuid.UUID
	requestUserID   *uuid.UUID
	detailUserID    *uuid.UUID
	accepted        *AcceptInput
	settleCalls     int
	releaseKind     string
	compensateCalls int
}

func (r *repositoryStub) CreateEntitlement(_ context.Context, input NewEntitlement, _ uuid.UUID) (Entitlement, error) {
	r.created = &input
	return Entitlement{ID: uuid.New(), UserID: input.UserID, Plan: input.Plan, ResourceDomain: input.ResourceDomain, GrantedTokens: input.GrantedTokens, BalanceTokens: input.GrantedTokens}, nil
}

func (r *repositoryStub) ListEntitlements(_ context.Context, query EntitlementQuery) (PageResult[Entitlement], error) {
	r.listedUserID = query.UserID
	return PageResult[Entitlement]{Items: []Entitlement{}}, nil
}

func (r *repositoryStub) ListLedger(context.Context, LedgerFilter) (PageResult[LedgerEvent], error) {
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
	r.compensateCalls++
	return Resolution{}, nil
}

func activePrincipal(role identity.Role, userID uuid.UUID) identity.Principal {
	return identity.Principal{UserID: userID, Role: role, Status: identity.StatusActive}
}

func TestAdministratorCreatesAnEntitlementWithExplicitScopeAndReason(t *testing.T) {
	repository := &repositoryStub{}
	service, err := NewService(repository)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	adminID, userID := uuid.New(), uuid.New()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.FixedZone("test", 8*60*60))
	created, err := service.CreateEntitlement(context.Background(), activePrincipal(identity.RoleAdministrator, adminID), NewEntitlement{
		IdempotencyKey: uuid.New(), RequestID: "quota-service-grant", UserID: userID, Plan: PlanCoding, ResourceDomain: ResourceFree, GrantedTokens: 10_000,
		StartsAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour), ConcurrencyLimit: 2, Note: "  team coding allocation  ",
	})
	if err != nil {
		t.Fatalf("CreateEntitlement() error = %v", err)
	}
	if created.BalanceTokens != 10_000 || repository.created == nil || repository.created.Note != "team coding allocation" {
		t.Fatalf("created = %#v, persisted = %#v", created, repository.created)
	}
	if repository.created.StartsAt.Location() != time.UTC || repository.created.ExpiresAt.Location() != time.UTC {
		t.Fatalf("entitlement timestamps were not normalized to UTC: %#v", repository.created)
	}
}

func TestMemberQuotaReadsAreScopedToTheAuthenticatedOwner(t *testing.T) {
	repository := &repositoryStub{}
	service, _ := NewService(repository)
	memberID := uuid.New()
	now := time.Now().UTC()
	if _, err := service.ListEntitlements(context.Background(), activePrincipal(identity.RoleMember, memberID), EntitlementQuery{}); err != nil {
		t.Fatalf("ListEntitlements() error = %v", err)
	}
	if repository.listedUserID == nil || *repository.listedUserID != memberID {
		t.Fatalf("repository user filter = %v, want %s", repository.listedUserID, memberID)
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
	configRevisionID := uuid.New()
	_, err := service.AcceptRequest(context.Background(), AcceptInput{
		RequestID: uuid.New(), UserID: uuid.New(), GatewayKeyID: uuid.New(), ModelID: uuid.New(), ResourceDomain: ResourceProfessional,
		ConfigRevisionID: &configRevisionID, RequestDigest: digest, IdempotencyKey: &key, ReservedTokens: 512,
	})
	if err != nil {
		t.Fatalf("AcceptRequest() error = %v", err)
	}
	digest[0] = 1
	if repository.accepted == nil || repository.accepted.ConfigRevisionID == nil || *repository.accepted.ConfigRevisionID != configRevisionID || *repository.accepted.IdempotencyKey != "retry-42" || repository.accepted.RequestDigest[0] != 0 {
		t.Fatalf("accepted input = %#v", repository.accepted)
	}
}

func TestAcceptRequestRejectsAMissingPublishedConfigurationRevision(t *testing.T) {
	repository := &repositoryStub{}
	service, _ := NewService(repository)
	_, err := service.AcceptRequest(context.Background(), AcceptInput{
		UserID: uuid.New(), GatewayKeyID: uuid.New(), ModelID: uuid.New(), ResourceDomain: ResourceProfessional,
		RequestDigest: make([]byte, 32), ReservedTokens: 512,
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("AcceptRequest() error = %v, want ErrInvalidInput", err)
	}
	if repository.accepted != nil {
		t.Fatal("request without a published configuration revision reached persistence")
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
