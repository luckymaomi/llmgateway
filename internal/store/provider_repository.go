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
	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/registry"
	db "github.com/luckymaomi/llmgateway/internal/store/db"
)

type RegistryRepository struct {
	connections              *Connections
	queries                  *db.Queries
	commitProviderMutation   func(context.Context, pgx.Tx) error
	commitCredentialMutation func(context.Context, pgx.Tx) error
}

func NewRegistryRepository(connections *Connections) *RegistryRepository {
	return &RegistryRepository{
		connections: connections,
		queries:     db.New(connections.Postgres),
		commitProviderMutation: func(ctx context.Context, tx pgx.Tx) error {
			return tx.Commit(ctx)
		},
		commitCredentialMutation: func(ctx context.Context, tx pgx.Tx) error {
			return tx.Commit(ctx)
		},
	}
}

func (r *RegistryRepository) ReplayProviderMutation(ctx context.Context, actorID uuid.UUID, mutation registry.ProviderMutation) (registry.Provider, bool, error) {
	operation, err := r.queries.GetProviderMutation(ctx, providerMutationLookup(actorID, mutation))
	if errors.Is(err, pgx.ErrNoRows) {
		return registry.Provider{}, false, nil
	}
	if err != nil {
		return registry.Provider{}, false, translateRegistryError(err)
	}
	provider, err := providerMutationResult(operation, mutation)
	return provider, true, err
}

func (r *RegistryRepository) CreateProvider(ctx context.Context, input registry.Provider, actorID uuid.UUID, mutation registry.ProviderMutation) (registry.Provider, error) {
	return r.executeProviderMutation(ctx, actorID, mutation, func(queries *db.Queries) (registry.Provider, error) {
		created, err := queries.CreateProvider(ctx, db.CreateProviderParams{Slug: input.Slug, Name: input.Name, Kind: string(input.Kind), BaseUrl: input.BaseURL, Enabled: input.Enabled, SourceUrl: input.SourceURL, VerifiedAt: optionalTimestamp(input.VerifiedAt)})
		if err != nil {
			return registry.Provider{}, translateRegistryError(err)
		}
		params := auditParams(&actorID, "provider.created", "provider", created.ID.String(), providerAuditDetail(nil, &created))
		params.RequestID = &mutation.RequestID
		if _, err := queries.CreateAuditEvent(ctx, params); err != nil {
			return registry.Provider{}, err
		}
		return providerFromDB(created), nil
	})
}

func (r *RegistryRepository) UpdateProvider(ctx context.Context, input registry.Provider, actorID uuid.UUID, mutation registry.ProviderMutation) (registry.Provider, error) {
	return r.executeProviderMutation(ctx, actorID, mutation, func(queries *db.Queries) (registry.Provider, error) {
		current, err := queries.GetProviderForUpdate(ctx, input.ID)
		if err != nil {
			return registry.Provider{}, translateRegistryError(err)
		}
		if !current.UpdatedAt.Time.Equal(input.UpdatedAt) {
			return registry.Provider{}, registry.ErrConflict
		}
		if current.Enabled && (current.Kind != string(input.Kind) || current.BaseUrl != input.BaseURL) {
			return registry.Provider{}, registry.ErrProviderEnabled
		}
		updated, err := queries.UpdateProvider(ctx, db.UpdateProviderParams{ID: input.ID, Name: input.Name, Kind: string(input.Kind), BaseUrl: input.BaseURL})
		if err != nil {
			return registry.Provider{}, translateRegistryError(err)
		}
		params := auditParams(&actorID, "provider.updated", "provider", input.ID.String(), providerAuditDetail(&current, &updated))
		params.RequestID = &mutation.RequestID
		if _, err := queries.CreateAuditEvent(ctx, params); err != nil {
			return registry.Provider{}, err
		}
		return providerFromDB(updated), nil
	})
}

func (r *RegistryRepository) SetProviderEnabled(ctx context.Context, providerID uuid.UUID, enabled bool, expectedUpdatedAt time.Time, actorID uuid.UUID, mutation registry.ProviderMutation) (registry.Provider, error) {
	return r.executeProviderMutation(ctx, actorID, mutation, func(queries *db.Queries) (registry.Provider, error) {
		current, err := queries.GetProviderForUpdate(ctx, providerID)
		if err != nil {
			return registry.Provider{}, translateRegistryError(err)
		}
		if !current.UpdatedAt.Time.Equal(expectedUpdatedAt) {
			return registry.Provider{}, registry.ErrConflict
		}
		updated, err := queries.SetProviderEnabled(ctx, db.SetProviderEnabledParams{ID: providerID, Enabled: enabled})
		if err != nil {
			return registry.Provider{}, translateRegistryError(err)
		}
		params := auditParams(&actorID, "provider.status_changed", "provider", providerID.String(), providerAuditDetail(&current, &updated))
		params.RequestID = &mutation.RequestID
		if _, err := queries.CreateAuditEvent(ctx, params); err != nil {
			return registry.Provider{}, err
		}
		return providerFromDB(updated), nil
	})
}

func (r *RegistryRepository) executeProviderMutation(ctx context.Context, actorID uuid.UUID, mutation registry.ProviderMutation, apply func(*db.Queries) (registry.Provider, error)) (registry.Provider, error) {
	tx, err := r.connections.Postgres.Begin(ctx)
	if err != nil {
		return registry.Provider{}, err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)

	operation, err := queries.ClaimProviderMutation(ctx, db.ClaimProviderMutationParams{
		ActorUserID: actorID, Action: string(mutation.Action), IdempotencyKey: mutation.IdempotencyKey,
		RequestFingerprint: mutation.RequestFingerprint, RequestID: mutation.RequestID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		existing, loadErr := queries.GetProviderMutation(ctx, providerMutationLookup(actorID, mutation))
		if loadErr != nil {
			return registry.Provider{}, translateRegistryError(loadErr)
		}
		return providerMutationResult(existing, mutation)
	}
	if err != nil {
		return registry.Provider{}, translateRegistryError(err)
	}

	result, err := apply(queries)
	if err != nil {
		return registry.Provider{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return registry.Provider{}, fmt.Errorf("encode provider mutation result: %w", err)
	}
	providerID := result.ID
	if _, err := queries.CompleteProviderMutation(ctx, db.CompleteProviderMutationParams{ProviderID: &providerID, Result: encoded, ID: operation.ID}); err != nil {
		return registry.Provider{}, err
	}
	if err := r.commitProviderMutation(ctx, tx); err != nil {
		return r.reconcileProviderMutation(ctx, actorID, mutation, err)
	}
	return result, nil
}

func (r *RegistryRepository) reconcileProviderMutation(ctx context.Context, actorID uuid.UUID, mutation registry.ProviderMutation, commitErr error) (registry.Provider, error) {
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	delay := 20 * time.Millisecond
	var reconciliationErr error
	for {
		operation, err := r.queries.GetProviderMutation(reconcileCtx, providerMutationLookup(actorID, mutation))
		if err == nil {
			return providerMutationResult(operation, mutation)
		}
		reconciliationErr = err
		timer := time.NewTimer(delay)
		select {
		case <-reconcileCtx.Done():
			timer.Stop()
			return registry.Provider{}, fmt.Errorf("%w: commit: %v; reconciliation: %v", registry.ErrOutcomeUnknown, commitErr, reconciliationErr)
		case <-timer.C:
		}
		if delay < 250*time.Millisecond {
			delay *= 2
		}
	}
}

func providerMutationLookup(actorID uuid.UUID, mutation registry.ProviderMutation) db.GetProviderMutationParams {
	return db.GetProviderMutationParams{ActorUserID: actorID, Action: string(mutation.Action), IdempotencyKey: mutation.IdempotencyKey}
}

func providerMutationResult(operation db.ProviderMutation, mutation registry.ProviderMutation) (registry.Provider, error) {
	if !bytes.Equal(operation.RequestFingerprint, mutation.RequestFingerprint) {
		return registry.Provider{}, registry.ErrIdempotencyConflict
	}
	var result registry.Provider
	if err := json.Unmarshal(operation.Result, &result); err != nil || result.ID == uuid.Nil || operation.ProviderID == nil || *operation.ProviderID != result.ID {
		return registry.Provider{}, fmt.Errorf("registry store: invalid provider mutation result")
	}
	return result, nil
}

func providerAuditDetail(before, after *db.Provider) map[string]any {
	detail := map[string]any{"before": nil, "after": nil}
	if before != nil {
		detail["before"] = providerAuditSummary(*before)
	}
	if after != nil {
		detail["after"] = providerAuditSummary(*after)
	}
	return detail
}

func providerAuditSummary(provider db.Provider) map[string]any {
	return map[string]any{
		"slug": provider.Slug, "name": provider.Name, "kind": provider.Kind,
		"base_url": provider.BaseUrl, "enabled": provider.Enabled,
		"source_url": provider.SourceUrl, "verified_at": timePointer(provider.VerifiedAt),
	}
}

func (r *RegistryRepository) ListProviders(ctx context.Context) ([]registry.Provider, error) {
	items, err := r.queries.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]registry.Provider, 0, len(items))
	for _, item := range items {
		result = append(result, providerFromDB(item))
	}
	return result, nil
}

func (r *RegistryRepository) GetProvider(ctx context.Context, id uuid.UUID) (registry.Provider, error) {
	provider, err := r.queries.GetProvider(ctx, id)
	if err != nil {
		return registry.Provider{}, translateRegistryError(err)
	}
	return providerFromDB(provider), nil
}

func providerFromDB(provider db.Provider) registry.Provider {
	return registry.Provider{ID: provider.ID, Slug: provider.Slug, Name: provider.Name, Kind: providers.Kind(provider.Kind), BaseURL: provider.BaseUrl, Enabled: provider.Enabled, SourceURL: provider.SourceUrl, VerifiedAt: timePointer(provider.VerifiedAt), CreatedAt: provider.CreatedAt.Time, UpdatedAt: provider.UpdatedAt.Time}
}
