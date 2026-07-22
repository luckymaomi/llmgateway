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
	"github.com/luckymaomi/llmgateway/internal/registry"
	db "github.com/luckymaomi/llmgateway/internal/store/db"
)

func (r *RegistryRepository) ReplayProviderPresetInstallation(ctx context.Context, actorID uuid.UUID, mutation registry.ProviderMutation) (registry.ProviderPresetInstallation, bool, error) {
	operation, err := r.queries.GetProviderMutation(ctx, providerMutationLookup(actorID, mutation))
	if errors.Is(err, pgx.ErrNoRows) {
		return registry.ProviderPresetInstallation{}, false, nil
	}
	if err != nil {
		return registry.ProviderPresetInstallation{}, false, translateRegistryError(err)
	}
	result, err := providerPresetInstallationResult(operation, mutation)
	return result, true, err
}

func (r *RegistryRepository) InstallProviderPreset(ctx context.Context, input registry.ProviderPresetInstallation, actorID uuid.UUID, mutation registry.ProviderMutation) (registry.ProviderPresetInstallation, error) {
	tx, err := r.connections.Postgres.Begin(ctx)
	if err != nil {
		return registry.ProviderPresetInstallation{}, err
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
			return registry.ProviderPresetInstallation{}, translateRegistryError(loadErr)
		}
		return providerPresetInstallationResult(existing, mutation)
	}
	if err != nil {
		return registry.ProviderPresetInstallation{}, translateRegistryError(err)
	}

	createdProvider, err := queries.CreateProviderWithID(ctx, db.CreateProviderWithIDParams{
		ID: input.Provider.ID, Slug: input.Provider.Slug, Name: input.Provider.Name, Kind: string(input.Provider.Kind),
		BaseUrl: input.Provider.BaseURL, Enabled: false, SourceUrl: input.Provider.SourceURL, VerifiedAt: optionalTimestamp(input.Provider.VerifiedAt),
	})
	if err != nil {
		return registry.ProviderPresetInstallation{}, translateRegistryError(err)
	}
	result := registry.ProviderPresetInstallation{PresetID: input.PresetID, Provider: providerFromDB(createdProvider), Models: make([]registry.Model, 0, len(input.Models))}
	for _, model := range input.Models {
		capabilities, err := json.Marshal(model.Capabilities)
		if err != nil {
			return registry.ProviderPresetInstallation{}, err
		}
		createdModel, err := queries.CreateModelWithID(ctx, db.CreateModelWithIDParams{
			ID: model.ID, ProviderID: createdProvider.ID, PublicName: model.PublicName, UpstreamName: model.UpstreamName,
			DisplayName: model.DisplayName, ResourceDomain: db.ResourceDomain(model.ResourceDomain), Capabilities: capabilities, Enabled: true,
		})
		if err != nil {
			return registry.ProviderPresetInstallation{}, translateRegistryError(err)
		}
		presented, err := modelFromDB(createdModel, createdProvider.Slug, createdProvider.Name)
		if err != nil {
			return registry.ProviderPresetInstallation{}, err
		}
		result.Models = append(result.Models, presented)
		if _, err := queries.CreateAuditEvent(ctx, auditParams(&actorID, "model.created", "model", createdModel.ID.String(), map[string]any{"public_name": createdModel.PublicName, "preset_id": input.PresetID})); err != nil {
			return registry.ProviderPresetInstallation{}, err
		}
	}
	providerAudit := auditParams(&actorID, "provider.preset_installed", "provider", createdProvider.ID.String(), map[string]any{"preset_id": input.PresetID, "model_count": len(result.Models)})
	providerAudit.RequestID = &mutation.RequestID
	if _, err := queries.CreateAuditEvent(ctx, providerAudit); err != nil {
		return registry.ProviderPresetInstallation{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return registry.ProviderPresetInstallation{}, fmt.Errorf("encode Provider preset result: %w", err)
	}
	providerID := result.Provider.ID
	if _, err := queries.CompleteProviderMutation(ctx, db.CompleteProviderMutationParams{ProviderID: &providerID, Result: encoded, ID: operation.ID}); err != nil {
		return registry.ProviderPresetInstallation{}, err
	}
	if err := r.commitProviderMutation(ctx, tx); err != nil {
		return r.reconcileProviderPresetInstallation(ctx, actorID, mutation, err)
	}
	return result, nil
}

func (r *RegistryRepository) reconcileProviderPresetInstallation(ctx context.Context, actorID uuid.UUID, mutation registry.ProviderMutation, commitErr error) (registry.ProviderPresetInstallation, error) {
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	delay := 20 * time.Millisecond
	var reconciliationErr error
	for {
		operation, err := r.queries.GetProviderMutation(reconcileCtx, providerMutationLookup(actorID, mutation))
		if err == nil {
			return providerPresetInstallationResult(operation, mutation)
		}
		reconciliationErr = err
		timer := time.NewTimer(delay)
		select {
		case <-reconcileCtx.Done():
			timer.Stop()
			return registry.ProviderPresetInstallation{}, fmt.Errorf("%w: commit: %v; reconciliation: %v", registry.ErrOutcomeUnknown, commitErr, reconciliationErr)
		case <-timer.C:
		}
		if delay < 250*time.Millisecond {
			delay *= 2
		}
	}
}

func providerPresetInstallationResult(operation db.ProviderMutation, mutation registry.ProviderMutation) (registry.ProviderPresetInstallation, error) {
	if !bytes.Equal(operation.RequestFingerprint, mutation.RequestFingerprint) {
		return registry.ProviderPresetInstallation{}, registry.ErrIdempotencyConflict
	}
	var result registry.ProviderPresetInstallation
	if err := json.Unmarshal(operation.Result, &result); err != nil || result.PresetID == "" || result.Provider.ID == uuid.Nil || operation.ProviderID == nil || *operation.ProviderID != result.Provider.ID || len(result.Models) == 0 {
		return registry.ProviderPresetInstallation{}, fmt.Errorf("registry store: invalid Provider preset mutation result")
	}
	return result, nil
}
