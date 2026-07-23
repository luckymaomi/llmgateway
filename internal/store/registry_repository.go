package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/registry"
	db "github.com/luckymaomi/llmgateway/internal/store/db"
)

func (r *RegistryRepository) ListModels(ctx context.Context) ([]registry.Model, error) {
	rows, err := r.queries.ListModels(ctx)
	if err != nil {
		return nil, translateRegistryError(err)
	}
	items := make([]registry.Model, 0, len(rows))
	for _, row := range rows {
		model, err := modelFromParts(row.ID, row.ProviderID, row.ProviderSlug, row.ProviderName, row.PublicName, row.UpstreamName, row.DisplayName, row.Capabilities, row.CreatedAt.Time, row.UpdatedAt.Time)
		if err != nil {
			return nil, err
		}
		items = append(items, model)
	}
	return items, nil
}

func (r *RegistryRepository) CreateResourcePool(ctx context.Context, input registry.NewResourcePool, actorID uuid.UUID, mutation registry.Mutation) (registry.ResourcePool, error) {
	return r.executeResourcePoolMutation(ctx, actorID, mutation, func(queries *db.Queries) (registry.ResourcePool, error) {
		created, err := queries.CreateResourcePool(ctx, db.CreateResourcePoolParams{ProviderID: input.ProviderID, Slug: input.Slug, Name: input.Name})
		if err != nil {
			return registry.ResourcePool{}, translateRegistryError(err)
		}
		for _, modelID := range input.ModelIDs {
			if _, err := queries.GetModelForCredentialBinding(ctx, db.GetModelForCredentialBindingParams{ID: modelID, ResourcePoolID: created.ID}); err == nil {
				return registry.ResourcePool{}, registry.ErrConflict
			}
			if err := queries.BindResourcePoolModel(ctx, db.BindResourcePoolModelParams{ResourcePoolID: created.ID, ModelID: modelID}); err != nil {
				return registry.ResourcePool{}, translateRegistryError(err)
			}
		}
		return resourcePoolByID(ctx, queries, created.ID)
	})
}

func (r *RegistryRepository) UpdateResourcePool(ctx context.Context, change registry.ResourcePoolChange, actorID uuid.UUID, mutation registry.Mutation) (registry.ResourcePool, error) {
	return r.executeResourcePoolMutation(ctx, actorID, mutation, func(queries *db.Queries) (registry.ResourcePool, error) {
		if _, err := queries.UpdateResourcePool(ctx, db.UpdateResourcePoolParams{Name: change.Name, ID: change.ID, ExpectedUpdatedAt: timestamp(change.ExpectedUpdatedAt)}); err != nil {
			return registry.ResourcePool{}, translateRegistryError(err)
		}
		return resourcePoolByID(ctx, queries, change.ID)
	})
}

func (r *RegistryRepository) SetResourcePoolStatus(ctx context.Context, id uuid.UUID, status registry.ResourcePoolStatus, expectedUpdatedAt time.Time, actorID uuid.UUID, mutation registry.Mutation) (registry.ResourcePool, error) {
	return r.executeResourcePoolMutation(ctx, actorID, mutation, func(queries *db.Queries) (registry.ResourcePool, error) {
		if _, err := queries.SetResourcePoolStatus(ctx, db.SetResourcePoolStatusParams{Status: db.ResourcePoolStatus(status), ID: id, ExpectedUpdatedAt: timestamp(expectedUpdatedAt)}); err != nil {
			return registry.ResourcePool{}, translateRegistryError(err)
		}
		return resourcePoolByID(ctx, queries, id)
	})
}

func (r *RegistryRepository) executeResourcePoolMutation(ctx context.Context, actorID uuid.UUID, mutation registry.Mutation, apply func(*db.Queries) (registry.ResourcePool, error)) (registry.ResourcePool, error) {
	tx, err := r.connections.Postgres.Begin(ctx)
	if err != nil {
		return registry.ResourcePool{}, err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	operation, err := queries.ClaimResourcePoolMutation(ctx, db.ClaimResourcePoolMutationParams{ActorUserID: actorID, Action: mutation.Action, IdempotencyKey: mutation.IdempotencyKey, RequestFingerprint: mutation.RequestFingerprint, RequestID: mutation.RequestID})
	if errors.Is(err, pgx.ErrNoRows) {
		existing, loadErr := queries.GetResourcePoolMutation(ctx, resourcePoolMutationLookup(actorID, mutation))
		if loadErr != nil {
			return registry.ResourcePool{}, translateRegistryError(loadErr)
		}
		return resourcePoolMutationResult(existing, mutation)
	}
	if err != nil {
		return registry.ResourcePool{}, translateRegistryError(err)
	}
	result, err := apply(queries)
	if err != nil {
		return registry.ResourcePool{}, err
	}
	audit := auditParams(&actorID, mutation.Action, "resource_pool", result.ID.String(), map[string]any{"resource_pool_id": result.ID, "status": result.Status, "provider_id": result.ProviderID})
	audit.RequestID = &mutation.RequestID
	if _, err := queries.CreateAuditEvent(ctx, audit); err != nil {
		return registry.ResourcePool{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return registry.ResourcePool{}, err
	}
	if _, err := queries.CompleteResourcePoolMutation(ctx, db.CompleteResourcePoolMutationParams{ResourcePoolID: &result.ID, Result: encoded, ID: operation.ID}); err != nil {
		return registry.ResourcePool{}, err
	}
	if err := r.commitResourcePoolMutation(ctx, tx); err != nil {
		return r.reconcileResourcePoolMutation(ctx, actorID, mutation, err)
	}
	return result, nil
}

func (r *RegistryRepository) reconcileResourcePoolMutation(ctx context.Context, actorID uuid.UUID, mutation registry.Mutation, commitErr error) (registry.ResourcePool, error) {
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	for delay := 20 * time.Millisecond; ; delay = minDuration(delay*2, 250*time.Millisecond) {
		operation, err := r.queries.GetResourcePoolMutation(reconcileCtx, resourcePoolMutationLookup(actorID, mutation))
		if err == nil {
			return resourcePoolMutationResult(operation, mutation)
		}
		if !waitForReconcile(reconcileCtx, delay) {
			return registry.ResourcePool{}, fmt.Errorf("%w: commit: %v; reconciliation: %v", registry.ErrOutcomeUnknown, commitErr, err)
		}
	}
}

func resourcePoolMutationLookup(actorID uuid.UUID, mutation registry.Mutation) db.GetResourcePoolMutationParams {
	return db.GetResourcePoolMutationParams{ActorUserID: actorID, Action: mutation.Action, IdempotencyKey: mutation.IdempotencyKey}
}

func resourcePoolMutationResult(operation db.ResourcePoolMutation, mutation registry.Mutation) (registry.ResourcePool, error) {
	if !bytes.Equal(operation.RequestFingerprint, mutation.RequestFingerprint) {
		return registry.ResourcePool{}, registry.ErrIdempotencyConflict
	}
	var result registry.ResourcePool
	if err := json.Unmarshal(operation.Result, &result); err != nil || operation.ResourcePoolID == nil || *operation.ResourcePoolID != result.ID {
		return registry.ResourcePool{}, fmt.Errorf("registry store: invalid resource pool mutation result")
	}
	return result, nil
}

func (r *RegistryRepository) ListResourcePools(ctx context.Context, includeRetired bool) ([]registry.ResourcePool, error) {
	rows, err := r.queries.ListResourcePools(ctx, includeRetired)
	if err != nil {
		return nil, translateRegistryError(err)
	}
	items := make([]registry.ResourcePool, 0, len(rows))
	for _, row := range rows {
		models, err := resourcePoolModels(ctx, r.queries, row.ID)
		if err != nil {
			return nil, err
		}
		items = append(items, registry.ResourcePool{
			ID: row.ID, ProviderID: row.ProviderID, ProviderCatalogID: row.CatalogID, ProviderSlug: row.ProviderSlug,
			ProviderName: row.ProviderName, ProviderKind: providers.Kind(row.ProviderKind), ProviderBaseURL: row.ProviderBaseUrl,
			Slug: row.Slug, Name: row.Name, Status: registry.ResourcePoolStatus(row.Status), Models: models,
			ModelCount: row.ModelCount, CredentialCount: row.CredentialCount, ActiveCredentialCount: row.ActiveCredentialCount,
			RetiredAt: timePointer(row.RetiredAt), CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
		})
	}
	return items, nil
}

func (r *RegistryRepository) GetResourcePool(ctx context.Context, id uuid.UUID) (registry.ResourcePool, error) {
	return resourcePoolByID(ctx, r.queries, id)
}

func resourcePoolByID(ctx context.Context, queries *db.Queries, id uuid.UUID) (registry.ResourcePool, error) {
	row, err := queries.GetResourcePool(ctx, id)
	if err != nil {
		return registry.ResourcePool{}, translateRegistryError(err)
	}
	models, err := resourcePoolModels(ctx, queries, id)
	if err != nil {
		return registry.ResourcePool{}, err
	}
	return registry.ResourcePool{
		ID: row.ID, ProviderID: row.ProviderID, ProviderCatalogID: row.CatalogID, ProviderSlug: row.ProviderSlug,
		ProviderName: row.ProviderName, ProviderKind: providers.Kind(row.ProviderKind), ProviderBaseURL: row.ProviderBaseUrl,
		Slug: row.Slug, Name: row.Name, Status: registry.ResourcePoolStatus(row.Status), Models: models, ModelCount: int64(len(models)),
		RetiredAt: timePointer(row.RetiredAt), CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
	}, nil
}

func resourcePoolModels(ctx context.Context, queries *db.Queries, id uuid.UUID) ([]registry.Model, error) {
	rows, err := queries.ListResourcePoolModels(ctx, id)
	if err != nil {
		return nil, translateRegistryError(err)
	}
	items := make([]registry.Model, 0, len(rows))
	for _, row := range rows {
		model, err := modelFromParts(row.ID, row.ProviderID, row.ProviderSlug, row.ProviderName, row.PublicName, row.UpstreamName, row.DisplayName, row.Capabilities, row.CreatedAt.Time, row.UpdatedAt.Time)
		if err != nil {
			return nil, err
		}
		items = append(items, model)
	}
	return items, nil
}

func modelFromParts(id, providerID uuid.UUID, providerSlug, providerName, publicName, upstreamName, displayName string, encoded []byte, createdAt, updatedAt time.Time) (registry.Model, error) {
	var capabilities registry.ModelCapabilities
	if err := json.Unmarshal(encoded, &capabilities); err != nil {
		return registry.Model{}, fmt.Errorf("decode model capabilities: %w", err)
	}
	return registry.Model{ID: id, ProviderID: providerID, ProviderSlug: providerSlug, ProviderName: providerName, PublicName: publicName, UpstreamName: upstreamName, DisplayName: displayName, Capabilities: capabilities, CreatedAt: createdAt.UTC(), UpdatedAt: updatedAt.UTC()}, nil
}

func translateRegistryError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return registry.ErrNotFound
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) {
		switch databaseError.Code {
		case "23503":
			return registry.ErrNotFound
		case "23505", "40001":
			return registry.ErrConflict
		}
	}
	return fmt.Errorf("registry store: %w", err)
}
