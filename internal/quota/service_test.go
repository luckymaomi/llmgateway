package quota

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/identity"
)

type repositoryStub struct {
	authorized      bool
	created         *NewEntitlement
	listedUserID    *uuid.UUID
	accepted        *AcceptInput
	settleCalls     int
	releaseKind     string
	compensateCalls int
}

func (r *repositoryStub) AuthorizeModel(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	r.authorized = true
	return nil
}

func (r *repositoryStub) RevokeModel(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
	return nil
}

func (r *repositoryStub) ListModelAuthorizations(context.Context, uuid.UUID) ([]ModelAuthorization, error) {
	return []ModelAuthorization{}, nil
}

func (r *repositoryStub) CreateEntitlement(_ context.Context, input NewEntitlement, _ uuid.UUID) (Entitlement, error) {
	r.created = &input
	return Entitlement{ID: uuid.New(), UserID: input.UserID, Plan: input.Plan, ResourceDomain: input.ResourceDomain, GrantedTokens: input.GrantedTokens, BalanceTokens: input.GrantedTokens}, nil
}

func (r *repositoryStub) ListEntitlements(_ context.Context, userID *uuid.UUID, _ Page) ([]Entitlement, error) {
	r.listedUserID = userID
	return []Entitlement{}, nil
}

func (r *repositoryStub) ListLedger(context.Context, LedgerFilter) ([]LedgerEvent, error) {
	return []LedgerEvent{}, nil
}

func (r *repositoryStub) AcceptRequest(_ context.Context, input AcceptInput) (AcceptedRequest, error) {
	r.accepted = &input
	return AcceptedRequest{}, nil
}

func (r *repositoryStub) Settle(context.Context, uuid.UUID, int64, int64, UsageSource) (Resolution, error) {
	r.settleCalls++
	return Resolution{}, nil
}

func (r *repositoryStub) Release(_ context.Context, _ uuid.UUID, kind, _ string) (Resolution, error) {
	r.releaseKind = kind
	return Resolution{}, nil
}

func (r *repositoryStub) Compensate(context.Context, uuid.UUID, int64, int64, UsageSource, string, string) (Resolution, error) {
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
		IdempotencyKey: uuid.New(), UserID: userID, Plan: PlanCoding, ResourceDomain: ResourceFree, GrantedTokens: 10_000,
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
	if _, err := service.ListEntitlements(context.Background(), activePrincipal(identity.RoleMember, memberID), nil, Page{}); err != nil {
		t.Fatalf("ListEntitlements() error = %v", err)
	}
	if repository.listedUserID == nil || *repository.listedUserID != memberID {
		t.Fatalf("repository user filter = %v, want %s", repository.listedUserID, memberID)
	}
}

func TestAcceptRequestPassesAStableDigestAndNormalizedIdempotencyKey(t *testing.T) {
	repository := &repositoryStub{}
	service, _ := NewService(repository)
	digest := make([]byte, 32)
	key := "  retry-42  "
	_, err := service.AcceptRequest(context.Background(), AcceptInput{
		UserID: uuid.New(), GatewayKeyID: uuid.New(), ModelID: uuid.New(), ResourceDomain: ResourceProfessional,
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

func TestUnknownUsageKeepsTheReservationForRecovery(t *testing.T) {
	repository := &repositoryStub{}
	service, _ := NewService(repository)
	_, err := service.Settle(context.Background(), uuid.New(), 0, 0, UsageUnknown)
	if !errors.Is(err, ErrUsageUnknown) {
		t.Fatalf("Settle() error = %v, want ErrUsageUnknown", err)
	}
	if repository.settleCalls != 0 {
		t.Fatalf("repository settle calls = %d, want 0", repository.settleCalls)
	}
}

func TestRepeatedAdministrativeModelAuthorizationUsesTheQuotaOwner(t *testing.T) {
	repository := &repositoryStub{}
	service, _ := NewService(repository)
	if err := service.AuthorizeModel(context.Background(), activePrincipal(identity.RoleAdministrator, uuid.New()), uuid.New(), uuid.New()); err != nil {
		t.Fatalf("AuthorizeModel() error = %v", err)
	}
	if !repository.authorized {
		t.Fatal("quota repository did not receive the model authorization")
	}
}
