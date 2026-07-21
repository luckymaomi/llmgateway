package requestflow

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

type CatalogService struct {
	repository CatalogRepository
}

func NewCatalogService(repository CatalogRepository) (*CatalogService, error) {
	if repository == nil {
		return nil, errors.New("catalog repository is required")
	}
	return &CatalogService{repository: repository}, nil
}

func (s *CatalogService) Models(ctx context.Context, gatewayKeyID uuid.UUID) ([]Model, error) {
	if gatewayKeyID == uuid.Nil {
		return nil, ErrModelNotAuthorized
	}
	return s.repository.ListPublishedModels(ctx, gatewayKeyID)
}
