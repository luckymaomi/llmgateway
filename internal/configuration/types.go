package configuration

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidInput        = errors.New("invalid configuration")
	ErrConflict            = errors.New("configuration conflict")
	ErrNotFound            = errors.New("configuration not found")
	ErrForbidden           = errors.New("configuration operation forbidden")
	ErrIdempotencyConflict = errors.New("configuration idempotency key conflict")
	ErrOutcomeUnknown      = errors.New("configuration operation outcome is unknown")
)

type MutationAction string

const (
	MutationCapture  MutationAction = "configuration.capture"
	MutationPublish  MutationAction = "configuration.publish"
	MutationRollback MutationAction = "configuration.rollback"
)

type MutationRequest struct {
	IdempotencyKey uuid.UUID
	RequestID      string
}

type Mutation struct {
	Action             MutationAction
	IdempotencyKey     uuid.UUID
	RequestFingerprint []byte
	RequestID          string
}

type Revision struct {
	ID          uuid.UUID      `json:"id"`
	Revision    int64          `json:"revision"`
	Checksum    string         `json:"checksum"`
	Catalog     CatalogSummary `json:"catalog"`
	CreatedBy   uuid.UUID      `json:"created_by"`
	CreatedAt   time.Time      `json:"created_at"`
	PublishedAt *time.Time     `json:"published_at,omitempty"`
	PublishedBy *uuid.UUID     `json:"published_by,omitempty"`
}

type CatalogSummary struct {
	ProviderCount   int64 `json:"provider_count"`
	ModelCount      int64 `json:"model_count"`
	CredentialCount int64 `json:"credential_count"`
	RouteCount      int64 `json:"route_count"`
}

type Catalog struct {
	Providers   []CatalogProvider   `json:"providers"`
	Models      []CatalogModel      `json:"models"`
	Credentials []CatalogCredential `json:"credentials"`
	Routes      []CatalogRoute      `json:"routes"`
}

type CatalogProvider struct {
	ID      uuid.UUID `json:"id"`
	Slug    string    `json:"slug"`
	Name    string    `json:"name"`
	Kind    string    `json:"kind"`
	BaseURL string    `json:"base_url"`
}

type CatalogModel struct {
	ID             uuid.UUID       `json:"id"`
	ProviderID     uuid.UUID       `json:"provider_id"`
	PublicName     string          `json:"public_name"`
	UpstreamName   string          `json:"upstream_name"`
	DisplayName    string          `json:"display_name"`
	ResourceDomain string          `json:"resource_domain"`
	Capabilities   json.RawMessage `json:"capabilities"`
	CreatedAt      time.Time       `json:"created_at"`
}

type CatalogCredential struct {
	ID               uuid.UUID `json:"id"`
	ProviderID       uuid.UUID `json:"provider_id"`
	ResourceDomain   string    `json:"resource_domain"`
	RPMLimit         *int32    `json:"rpm_limit,omitempty"`
	TPMLimit         *int64    `json:"tpm_limit,omitempty"`
	ConcurrencyLimit *int32    `json:"concurrency_limit,omitempty"`
}

type CatalogRoute struct {
	ModelID      uuid.UUID `json:"model_id"`
	CredentialID uuid.UUID `json:"credential_id"`
	Priority     int32     `json:"priority"`
	Weight       int32     `json:"weight"`
}

type Active struct {
	Revision  Revision  `json:"revision"`
	Version   int64     `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Repository interface {
	ReplayRevisionMutation(context.Context, uuid.UUID, Mutation) (Revision, bool, error)
	CreateRevision(context.Context, uuid.UUID, Mutation) (Revision, error)
	GetRevision(context.Context, uuid.UUID) (Revision, error)
	ListRevisions(context.Context, int32, int32) ([]Revision, error)
	Active(context.Context) (Active, error)
	ActiveCatalog(context.Context) (Active, Catalog, error)
	ReplayPublishMutation(context.Context, uuid.UUID, Mutation) (Active, bool, error)
	Publish(context.Context, uuid.UUID, int64, uuid.UUID, Mutation) (Active, error)
}
