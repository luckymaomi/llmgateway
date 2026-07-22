package operations

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/identity"
)

type scopeRepository struct {
	memberAccessUserID uuid.UUID
	requestUserIDs     []*uuid.UUID
}

func (r *scopeRepository) AdministratorResources(context.Context, time.Time) (AdministratorResources, error) {
	return AdministratorResources{}, nil
}

func (r *scopeRepository) MemberAccess(_ context.Context, userID uuid.UUID, _ time.Time) (MemberAccess, error) {
	r.memberAccessUserID = userID
	return MemberAccess{}, nil
}

func (r *scopeRepository) RequestSummary(_ context.Context, userID *uuid.UUID, _, _ time.Time) (RequestSummary, error) {
	r.capture(userID)
	return RequestSummary{}, nil
}

func (r *scopeRepository) RequestTrend(_ context.Context, userID *uuid.UUID, _, _ time.Time) ([]TrendPoint, error) {
	r.capture(userID)
	return nil, nil
}

func (r *scopeRepository) RequestErrors(_ context.Context, userID *uuid.UUID, _, _ time.Time) ([]ErrorCount, error) {
	r.capture(userID)
	return nil, nil
}

func (r *scopeRepository) capture(userID *uuid.UUID) {
	if userID == nil {
		r.requestUserIDs = append(r.requestUserIDs, nil)
		return
	}
	value := *userID
	r.requestUserIDs = append(r.requestUserIDs, &value)
}

func TestMemberOverviewScopesEveryFactToTheCurrentMember(t *testing.T) {
	repository := &scopeRepository{}
	service, err := NewService(repository)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	memberID := uuid.New()
	_, err = service.Overview(context.Background(), identity.Principal{
		UserID: memberID,
		Role:   identity.RoleMember,
		Status: identity.StatusActive,
	})
	if err != nil {
		t.Fatalf("Overview() error = %v", err)
	}
	if repository.memberAccessUserID != memberID {
		t.Fatalf("MemberAccess() user = %s, want %s", repository.memberAccessUserID, memberID)
	}
	if len(repository.requestUserIDs) != 3 {
		t.Fatalf("scoped request queries = %d, want 3", len(repository.requestUserIDs))
	}
	for index, userID := range repository.requestUserIDs {
		if userID == nil || *userID != memberID {
			t.Fatalf("request query %d user = %v, want %s", index, userID, memberID)
		}
	}
}
