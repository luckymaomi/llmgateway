package configuration

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/identity"
)

type Service struct {
	repository Repository
}

func NewService(repository Repository) (*Service, error) {
	if repository == nil {
		return nil, fmt.Errorf("configuration repository is required")
	}
	return &Service{repository: repository}, nil
}

func (s *Service) CreateRevision(ctx context.Context, actor identity.Principal, request MutationRequest) (Revision, error) {
	if !actor.CanOperateProviders() {
		return Revision{}, ErrForbidden
	}
	mutation, err := newMutation(MutationCapture, request, uuid.Nil, nil)
	if err != nil {
		return Revision{}, err
	}
	if replayed, found, err := s.repository.ReplayRevisionMutation(ctx, actor.UserID, mutation); err != nil || found {
		return replayed, err
	}
	return s.repository.CreateRevision(ctx, actor.UserID, mutation)
}

func (s *Service) Publish(ctx context.Context, actor identity.Principal, revisionID uuid.UUID, expectedActiveVersion int64, action MutationAction, request MutationRequest) (Active, error) {
	if !actor.CanOperateProviders() {
		return Active{}, ErrForbidden
	}
	if revisionID == uuid.Nil || expectedActiveVersion < 0 || action != MutationPublish && action != MutationRollback {
		return Active{}, ErrInvalidInput
	}
	mutation, err := newMutation(action, request, revisionID, &expectedActiveVersion)
	if err != nil {
		return Active{}, err
	}
	if replayed, found, err := s.repository.ReplayPublishMutation(ctx, actor.UserID, mutation); err != nil || found {
		return replayed, err
	}
	revision, err := s.repository.GetRevision(ctx, revisionID)
	if err != nil {
		return Active{}, err
	}
	if revision.Catalog.ModelCount == 0 || revision.Catalog.RouteCount == 0 {
		return Active{}, ErrInvalidInput
	}
	return s.repository.Publish(ctx, revisionID, expectedActiveVersion, actor.UserID, mutation)
}

func (s *Service) Active(ctx context.Context, actor identity.Principal) (Active, error) {
	if actor.Status != identity.StatusActive {
		return Active{}, ErrForbidden
	}
	return s.repository.Active(ctx)
}

func (s *Service) ActiveCatalog(ctx context.Context, actor identity.Principal) (Active, Catalog, error) {
	if !actor.CanOperateProviders() {
		return Active{}, Catalog{}, ErrForbidden
	}
	return s.repository.ActiveCatalog(ctx)
}

func (s *Service) ListRevisions(ctx context.Context, actor identity.Principal, offset, size int32) ([]Revision, error) {
	if !actor.CanOperateProviders() {
		return nil, ErrForbidden
	}
	if offset < 0 {
		offset = 0
	}
	if size < 1 {
		size = 50
	}
	if size > 200 {
		size = 200
	}
	return s.repository.ListRevisions(ctx, offset, size)
}
