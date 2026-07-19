package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/luckymaomi/llmgateway/internal/configuration"
	db "github.com/luckymaomi/llmgateway/internal/store/db"
)

type ConfigurationRepository struct {
	connections *Connections
	queries     *db.Queries
}

func NewConfigurationRepository(connections *Connections) *ConfigurationRepository {
	return &ConfigurationRepository{connections: connections, queries: db.New(connections.Postgres)}
}

func (r *ConfigurationRepository) CreateRevision(ctx context.Context, document configuration.Document, checksum string, actorID uuid.UUID) (configuration.Revision, error) {
	encoded, err := json.Marshal(document)
	if err != nil {
		return configuration.Revision{}, err
	}
	revision, err := r.queries.CreateConfigRevision(ctx, db.CreateConfigRevisionParams{Document: encoded, Checksum: checksum, CreatedBy: actorID})
	if err != nil {
		return configuration.Revision{}, translateConfigurationError(err)
	}
	if _, err := r.queries.CreateAuditEvent(ctx, auditParams(&actorID, "configuration.revision_created", "config_revision", revision.ID.String(), map[string]any{"checksum": checksum})); err != nil {
		return configuration.Revision{}, err
	}
	return revisionFromDB(revision)
}

func (r *ConfigurationRepository) GetRevision(ctx context.Context, id uuid.UUID) (configuration.Revision, error) {
	revision, err := r.queries.GetConfigRevision(ctx, id)
	if err != nil {
		return configuration.Revision{}, translateConfigurationError(err)
	}
	return revisionFromDB(revision)
}

func (r *ConfigurationRepository) ListRevisions(ctx context.Context, offset, size int32) ([]configuration.Revision, error) {
	items, err := r.queries.ListConfigRevisions(ctx, db.ListConfigRevisionsParams{PageOffset: offset, PageSize: size})
	if err != nil {
		return nil, err
	}
	result := make([]configuration.Revision, 0, len(items))
	for _, item := range items {
		revision, err := revisionFromDB(item)
		if err != nil {
			return nil, err
		}
		result = append(result, revision)
	}
	return result, nil
}

func (r *ConfigurationRepository) Active(ctx context.Context) (configuration.Active, error) {
	active, err := r.queries.GetActiveConfig(ctx)
	if err != nil {
		return configuration.Active{}, translateConfigurationError(err)
	}
	return activeFromDB(active)
}

func (r *ConfigurationRepository) Publish(ctx context.Context, revisionID uuid.UUID, expectedVersion int64, actorID uuid.UUID) (configuration.Active, error) {
	tx, err := r.connections.Postgres.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return configuration.Active{}, err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	revision, err := queries.GetConfigRevision(ctx, revisionID)
	if err != nil {
		return configuration.Active{}, translateConfigurationError(err)
	}

	activeVersion := int64(1)
	active, lockErr := queries.LockActiveConfig(ctx)
	switch {
	case errors.Is(lockErr, pgx.ErrNoRows):
		if expectedVersion != 0 {
			return configuration.Active{}, configuration.ErrConflict
		}
		rows, err := queries.InitializeActiveConfig(ctx, revisionID)
		if err != nil {
			return configuration.Active{}, translateConfigurationError(err)
		}
		if rows != 1 {
			return configuration.Active{}, configuration.ErrConflict
		}
	case lockErr != nil:
		return configuration.Active{}, lockErr
	default:
		if active.Version != expectedVersion {
			return configuration.Active{}, configuration.ErrConflict
		}
		rows, err := queries.PublishConfigRevision(ctx, db.PublishConfigRevisionParams{RevisionID: revisionID, ExpectedVersion: expectedVersion})
		if err != nil {
			return configuration.Active{}, translateConfigurationError(err)
		}
		if rows != 1 {
			return configuration.Active{}, configuration.ErrConflict
		}
		activeVersion = active.Version + 1
	}

	if err := queries.MarkConfigPublished(ctx, db.MarkConfigPublishedParams{PublishedBy: &actorID, ID: revisionID}); err != nil {
		return configuration.Active{}, err
	}
	if err := queries.CreateConfigOutbox(ctx, db.CreateConfigOutboxParams{RevisionID: revisionID, ActiveVersion: activeVersion, Document: revision.Document}); err != nil {
		return configuration.Active{}, err
	}
	if _, err := queries.CreateAuditEvent(ctx, auditParams(&actorID, "configuration.published", "config_revision", revisionID.String(), map[string]any{"active_version": activeVersion, "previous_version": expectedVersion})); err != nil {
		return configuration.Active{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return configuration.Active{}, translateConfigurationError(err)
	}

	result, err := revisionFromDB(revision)
	if err != nil {
		return configuration.Active{}, err
	}
	now := time.Now().UTC()
	result.PublishedAt = &now
	result.PublishedBy = &actorID
	return configuration.Active{Revision: result, Version: activeVersion, UpdatedAt: now}, nil
}

func revisionFromDB(revision db.ConfigRevision) (configuration.Revision, error) {
	if revision.Revision == nil {
		return configuration.Revision{}, fmt.Errorf("configuration revision identity is missing")
	}
	var document configuration.Document
	if err := json.Unmarshal(revision.Document, &document); err != nil {
		return configuration.Revision{}, fmt.Errorf("decode configuration revision: %w", err)
	}
	return configuration.Revision{ID: revision.ID, Revision: *revision.Revision, Document: document, Checksum: revision.Checksum, CreatedBy: revision.CreatedBy, CreatedAt: revision.CreatedAt.Time, PublishedAt: timePointer(revision.PublishedAt), PublishedBy: revision.PublishedBy}, nil
}

func activeFromDB(active db.GetActiveConfigRow) (configuration.Active, error) {
	if active.Revision == nil {
		return configuration.Active{}, fmt.Errorf("active configuration revision identity is missing")
	}
	var document configuration.Document
	if err := json.Unmarshal(active.Document, &document); err != nil {
		return configuration.Active{}, fmt.Errorf("decode active configuration: %w", err)
	}
	return configuration.Active{Revision: configuration.Revision{ID: active.ID, Revision: *active.Revision, Document: document, Checksum: active.Checksum, CreatedBy: active.CreatedBy, CreatedAt: active.CreatedAt.Time, PublishedAt: timePointer(active.PublishedAt), PublishedBy: active.PublishedBy}, Version: active.ActiveVersion, UpdatedAt: active.ActiveUpdatedAt.Time}, nil
}

func translateConfigurationError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return configuration.ErrNotFound
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) {
		switch databaseError.Code {
		case "23503":
			return configuration.ErrNotFound
		case "23505", "40001":
			return configuration.ErrConflict
		}
	}
	return fmt.Errorf("configuration store: %w", err)
}
