package configuration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

func DefaultDocument() Document {
	return Document{
		Requests: RequestPolicy{MaxBodyBytes: 4 << 20, MaxContextTokens: 262144, MaxStreamSeconds: 900, QueueTimeoutMillis: 30000},
		Routing:  RoutingPolicy{MaxAttempts: 3, BaseBackoffMillis: 250, MaxBackoffMillis: 5000, AffinityTTLSeconds: 1800, CircuitOpenSeconds: 30},
		Audit:    AuditPolicy{ContentRetentionHours: 168, RequestRetentionDays: 90},
	}
}

func (s *Service) CreateRevision(ctx context.Context, actor identity.Principal, document Document) (Revision, error) {
	if !actor.CanOperateProviders() {
		return Revision{}, ErrForbidden
	}
	if err := document.Validate(); err != nil {
		return Revision{}, err
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return Revision{}, fmt.Errorf("encode configuration: %w", err)
	}
	checksum := sha256.Sum256(encoded)
	return s.repository.CreateRevision(ctx, document, hex.EncodeToString(checksum[:]), actor.UserID)
}

func (s *Service) Publish(ctx context.Context, actor identity.Principal, revisionID uuid.UUID, expectedVersion int64) (Active, error) {
	if !actor.CanOperateProviders() {
		return Active{}, ErrForbidden
	}
	if revisionID == uuid.Nil || expectedVersion < 0 {
		return Active{}, ErrInvalidInput
	}
	revision, err := s.repository.GetRevision(ctx, revisionID)
	if err != nil {
		return Active{}, err
	}
	if err := revision.Document.Validate(); err != nil {
		return Active{}, err
	}
	return s.repository.Publish(ctx, revisionID, expectedVersion, actor.UserID)
}

func (s *Service) Active(ctx context.Context, actor identity.Principal) (Active, error) {
	if actor.Status != identity.StatusActive {
		return Active{}, ErrForbidden
	}
	return s.repository.Active(ctx)
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

func (d Document) Validate() error {
	if d.Requests.MaxBodyBytes < 1024 || d.Requests.MaxBodyBytes > 64<<20 ||
		d.Requests.MaxContextTokens < 1024 || d.Requests.MaxContextTokens > 4_000_000 ||
		d.Requests.MaxStreamSeconds < 1 || d.Requests.MaxStreamSeconds > 3600 ||
		d.Requests.QueueTimeoutMillis < 0 || d.Requests.QueueTimeoutMillis > 300_000 {
		return ErrInvalidInput
	}
	if d.Routing.MaxAttempts < 1 || d.Routing.MaxAttempts > 5 ||
		d.Routing.BaseBackoffMillis < 10 || d.Routing.BaseBackoffMillis > d.Routing.MaxBackoffMillis ||
		d.Routing.MaxBackoffMillis > 60_000 || d.Routing.AffinityTTLSeconds < 0 || d.Routing.AffinityTTLSeconds > 86_400 ||
		d.Routing.CircuitOpenSeconds < 1 || d.Routing.CircuitOpenSeconds > 3600 {
		return ErrInvalidInput
	}
	if d.Audit.ContentRetentionHours < 0 || d.Audit.ContentRetentionHours > 8760 || d.Audit.RequestRetentionDays < 1 || d.Audit.RequestRetentionDays > 3650 {
		return ErrInvalidInput
	}
	return nil
}
