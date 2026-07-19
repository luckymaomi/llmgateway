package configuration

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidInput = errors.New("invalid configuration")
	ErrConflict     = errors.New("configuration conflict")
	ErrNotFound     = errors.New("configuration not found")
	ErrForbidden    = errors.New("configuration operation forbidden")
)

type Document struct {
	Requests RequestPolicy `json:"requests"`
	Routing  RoutingPolicy `json:"routing"`
	Audit    AuditPolicy   `json:"audit"`
}

type RequestPolicy struct {
	MaxBodyBytes       int64 `json:"max_body_bytes"`
	MaxContextTokens   int64 `json:"max_context_tokens"`
	MaxStreamSeconds   int64 `json:"max_stream_seconds"`
	QueueTimeoutMillis int64 `json:"queue_timeout_millis"`
}

type RoutingPolicy struct {
	MaxAttempts        int32 `json:"max_attempts"`
	BaseBackoffMillis  int64 `json:"base_backoff_millis"`
	MaxBackoffMillis   int64 `json:"max_backoff_millis"`
	AffinityTTLSeconds int64 `json:"affinity_ttl_seconds"`
	CircuitOpenSeconds int64 `json:"circuit_open_seconds"`
}

type AuditPolicy struct {
	ContentRetentionHours int64 `json:"content_retention_hours"`
	RequestRetentionDays  int64 `json:"request_retention_days"`
}

type Revision struct {
	ID          uuid.UUID  `json:"id"`
	Revision    int64      `json:"revision"`
	Document    Document   `json:"document"`
	Checksum    string     `json:"checksum"`
	CreatedBy   uuid.UUID  `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	PublishedBy *uuid.UUID `json:"published_by,omitempty"`
}

type Active struct {
	Revision  Revision  `json:"revision"`
	Version   int64     `json:"version"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Repository interface {
	CreateRevision(context.Context, Document, string, uuid.UUID) (Revision, error)
	GetRevision(context.Context, uuid.UUID) (Revision, error)
	ListRevisions(context.Context, int32, int32) ([]Revision, error)
	Active(context.Context) (Active, error)
	Publish(context.Context, uuid.UUID, int64, uuid.UUID) (Active, error)
}
