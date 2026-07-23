package operations

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/identity"
)

type Service struct {
	repository Repository
	now        func() time.Time
}

func NewService(repository Repository) (*Service, error) {
	if repository == nil {
		return nil, fmt.Errorf("operations repository is required")
	}
	return &Service{repository: repository, now: time.Now}, nil
}

func (s *Service) Overview(ctx context.Context, actor identity.Principal) (Overview, error) {
	if actor.Status != identity.StatusActive {
		return Overview{}, ErrForbidden
	}
	until := s.now().UTC().Truncate(time.Hour).Add(time.Hour)
	since := until.Add(-24 * time.Hour)
	if actor.CanManageUsers() {
		resources, err := s.repository.AdministratorResources(ctx, until)
		if err != nil {
			return Overview{}, err
		}
		requests, trend, errorsFound, err := s.requestFacts(ctx, nil, since, until)
		if err != nil {
			return Overview{}, err
		}
		return Overview{Administrator: &AdministratorOverview{Resources: resources, Requests: requests, Trend: trend, Errors: errorsFound}}, nil
	}
	if actor.Role != identity.RoleMember {
		return Overview{}, ErrForbidden
	}
	access, err := s.repository.MemberAccess(ctx, actor.UserID, until)
	if err != nil {
		return Overview{}, err
	}
	requests, trend, errorsFound, err := s.requestFacts(ctx, &actor.UserID, since, until)
	if err != nil {
		return Overview{}, err
	}
	return Overview{Member: &MemberOverview{Access: access, Requests: requests, Trend: trend, Errors: errorsFound}}, nil
}

func (s *Service) requestFacts(ctx context.Context, userID *uuid.UUID, since, until time.Time) (RequestSummary, []TrendPoint, []ErrorCount, error) {
	requests, err := s.repository.RequestSummary(ctx, userID, since, until)
	if err != nil {
		return RequestSummary{}, nil, nil, err
	}
	trend, err := s.repository.RequestTrend(ctx, userID, since, until)
	if err != nil {
		return RequestSummary{}, nil, nil, err
	}
	errorsFound, err := s.repository.RequestErrors(ctx, userID, since, until)
	if err != nil {
		return RequestSummary{}, nil, nil, err
	}
	return requests, trend, errorsFound, nil
}
