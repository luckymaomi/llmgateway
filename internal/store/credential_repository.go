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

func (r *RegistryRepository) CreateCredential(ctx context.Context, input registry.NewCredential, actorID uuid.UUID, mutation registry.Mutation) (registry.Credential, error) {
	return r.executeCredentialMutation(ctx, actorID, mutation, func(queries *db.Queries) (registry.Credential, error) {
		for _, binding := range input.ModelBindings {
			if _, err := queries.GetModelForCredentialBinding(ctx, db.GetModelForCredentialBindingParams{ID: binding.ModelID, ResourcePoolID: input.ResourcePoolID}); err != nil {
				return registry.Credential{}, translateRegistryError(err)
			}
		}
		created, err := queries.CreateCredential(ctx, db.CreateCredentialParams{
			ID: input.ID, ResourcePoolID: input.ResourcePoolID, Name: input.Name, EncryptedSecret: input.EncryptedSecret,
			RpmLimit: input.RPMLimit, TpmLimit: input.TPMLimit, ConcurrencyLimit: input.ConcurrencyLimit,
		})
		if err != nil {
			return registry.Credential{}, translateRegistryError(err)
		}
		if err := bindCredentialModels(ctx, queries, created.ID, input.ModelBindings); err != nil {
			return registry.Credential{}, err
		}
		return credentialByID(ctx, queries, created.ID)
	})
}

func (r *RegistryRepository) UpdateCredential(ctx context.Context, input registry.CredentialChange, actorID uuid.UUID, mutation registry.Mutation) (registry.Credential, error) {
	return r.executeCredentialMutation(ctx, actorID, mutation, func(queries *db.Queries) (registry.Credential, error) {
		current, err := queries.GetCredentialForUpdate(ctx, input.ID)
		if err != nil {
			return registry.Credential{}, translateRegistryError(err)
		}
		for _, binding := range input.ModelBindings {
			if _, err := queries.GetModelForCredentialBinding(ctx, db.GetModelForCredentialBindingParams{ID: binding.ModelID, ResourcePoolID: current.ResourcePoolID}); err != nil {
				return registry.Credential{}, translateRegistryError(err)
			}
		}
		if _, err := queries.UpdateCredential(ctx, db.UpdateCredentialParams{
			Name: input.Name, ReplaceSecret: input.ReplaceSecret, EncryptedSecret: input.EncryptedSecret,
			RpmLimit: input.RPMLimit, TpmLimit: input.TPMLimit, ConcurrencyLimit: input.ConcurrencyLimit,
			ID: input.ID, ExpectedUpdatedAt: timestamp(input.ExpectedUpdatedAt),
		}); err != nil {
			return registry.Credential{}, translateRegistryError(err)
		}
		if err := queries.DeleteCredentialModelBindings(ctx, input.ID); err != nil {
			return registry.Credential{}, translateRegistryError(err)
		}
		if err := bindCredentialModels(ctx, queries, input.ID, input.ModelBindings); err != nil {
			return registry.Credential{}, err
		}
		return credentialByID(ctx, queries, input.ID)
	})
}

func (r *RegistryRepository) SetCredentialStatus(ctx context.Context, id uuid.UUID, status registry.CredentialStatus, expectedUpdatedAt time.Time, actorID uuid.UUID, mutation registry.Mutation) (registry.Credential, error) {
	return r.executeCredentialMutation(ctx, actorID, mutation, func(queries *db.Queries) (registry.Credential, error) {
		if _, err := queries.SetCredentialStatus(ctx, db.SetCredentialStatusParams{Status: db.CredentialStatus(status), ID: id, ExpectedUpdatedAt: timestamp(expectedUpdatedAt)}); err != nil {
			return registry.Credential{}, translateRegistryError(err)
		}
		return credentialByID(ctx, queries, id)
	})
}

func (r *RegistryRepository) RetireCredential(ctx context.Context, id uuid.UUID, encryptedTombstone []byte, expectedUpdatedAt time.Time, actorID uuid.UUID, mutation registry.Mutation) (registry.Credential, error) {
	return r.executeCredentialMutation(ctx, actorID, mutation, func(queries *db.Queries) (registry.Credential, error) {
		if _, err := queries.RetireCredential(ctx, db.RetireCredentialParams{EncryptedTombstone: encryptedTombstone, ID: id, ExpectedUpdatedAt: timestamp(expectedUpdatedAt)}); err != nil {
			return registry.Credential{}, translateRegistryError(err)
		}
		return credentialByID(ctx, queries, id)
	})
}

func bindCredentialModels(ctx context.Context, queries *db.Queries, credentialID uuid.UUID, bindings []registry.CredentialModelBinding) error {
	for _, binding := range bindings {
		if err := queries.BindCredentialModel(ctx, db.BindCredentialModelParams{CredentialID: credentialID, ModelID: binding.ModelID, Priority: binding.Priority, Weight: binding.Weight}); err != nil {
			return translateRegistryError(err)
		}
	}
	return nil
}

func (r *RegistryRepository) executeCredentialMutation(ctx context.Context, actorID uuid.UUID, mutation registry.Mutation, apply func(*db.Queries) (registry.Credential, error)) (registry.Credential, error) {
	tx, err := r.connections.Postgres.Begin(ctx)
	if err != nil {
		return registry.Credential{}, err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	operation, err := queries.ClaimCredentialMutation(ctx, db.ClaimCredentialMutationParams{ActorUserID: actorID, Action: mutation.Action, IdempotencyKey: mutation.IdempotencyKey, RequestFingerprint: mutation.RequestFingerprint, RequestID: mutation.RequestID})
	if errors.Is(err, pgx.ErrNoRows) {
		existing, loadErr := queries.GetCredentialMutation(ctx, credentialMutationLookup(actorID, mutation))
		if loadErr != nil {
			return registry.Credential{}, translateRegistryError(loadErr)
		}
		return credentialMutationResult(existing, mutation)
	}
	if err != nil {
		return registry.Credential{}, translateRegistryError(err)
	}
	result, err := apply(queries)
	if err != nil {
		return registry.Credential{}, err
	}
	audit := auditParams(&actorID, mutation.Action, "provider_credential", result.ID.String(), map[string]any{
		"resource_pool_id": result.ResourcePoolID, "status": result.Status, "model_bindings": result.ModelBindings,
	})
	audit.RequestID = &mutation.RequestID
	if _, err := queries.CreateAuditEvent(ctx, audit); err != nil {
		return registry.Credential{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return registry.Credential{}, err
	}
	if _, err := queries.CompleteCredentialMutation(ctx, db.CompleteCredentialMutationParams{CredentialID: &result.ID, Result: encoded, ID: operation.ID}); err != nil {
		return registry.Credential{}, err
	}
	if err := r.commitCredentialMutation(ctx, tx); err != nil {
		return r.reconcileCredentialMutation(ctx, actorID, mutation, err)
	}
	return result, nil
}

func (r *RegistryRepository) reconcileCredentialMutation(ctx context.Context, actorID uuid.UUID, mutation registry.Mutation, commitErr error) (registry.Credential, error) {
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	for delay := 20 * time.Millisecond; ; delay = minDuration(delay*2, 250*time.Millisecond) {
		operation, err := r.queries.GetCredentialMutation(reconcileCtx, credentialMutationLookup(actorID, mutation))
		if err == nil {
			return credentialMutationResult(operation, mutation)
		}
		if !waitForReconcile(reconcileCtx, delay) {
			return registry.Credential{}, fmt.Errorf("%w: commit: %v; reconciliation: %v", registry.ErrOutcomeUnknown, commitErr, err)
		}
	}
}

func credentialMutationLookup(actorID uuid.UUID, mutation registry.Mutation) db.GetCredentialMutationParams {
	return db.GetCredentialMutationParams{ActorUserID: actorID, Action: mutation.Action, IdempotencyKey: mutation.IdempotencyKey}
}

func credentialMutationResult(operation db.CredentialMutation, mutation registry.Mutation) (registry.Credential, error) {
	if !bytes.Equal(operation.RequestFingerprint, mutation.RequestFingerprint) {
		return registry.Credential{}, registry.ErrIdempotencyConflict
	}
	var result registry.Credential
	if err := json.Unmarshal(operation.Result, &result); err != nil || operation.CredentialID == nil || *operation.CredentialID != result.ID {
		return registry.Credential{}, fmt.Errorf("registry store: invalid credential mutation result")
	}
	return result, nil
}

func (r *RegistryRepository) GetCredential(ctx context.Context, id uuid.UUID) (registry.Credential, error) {
	return credentialByID(ctx, r.queries, id)
}

func credentialByID(ctx context.Context, queries *db.Queries, id uuid.UUID) (registry.Credential, error) {
	row, err := queries.GetCredential(ctx, id)
	if err != nil {
		return registry.Credential{}, translateRegistryError(err)
	}
	bindings, err := credentialBindings(ctx, queries, id)
	if err != nil {
		return registry.Credential{}, err
	}
	return registry.Credential{
		ID: row.ID, ResourcePoolID: row.ResourcePoolID, ResourcePoolName: row.ResourcePoolName, ResourcePoolSlug: row.ResourcePoolSlug,
		ProviderID: row.ProviderID, ProviderName: row.ProviderName, ProviderKind: providers.Kind(row.ProviderKind), ProviderBaseURL: row.ProviderBaseUrl,
		Name: row.Name, Status: registry.CredentialStatus(row.Status), RPMLimit: row.RpmLimit, TPMLimit: row.TpmLimit, ConcurrencyLimit: row.ConcurrencyLimit,
		CooldownUntil: timePointer(row.CooldownUntil), ConsecutiveFailures: row.ConsecutiveFailures, LastSuccessAt: timePointer(row.LastSuccessAt), LastErrorKind: row.LastErrorKind,
		LastProbeAt: timePointer(row.LastProbeAt), LastProbeLatencyMs: row.LastProbeLatencyMs, LastProbeKind: row.LastProbeKind, LastProbeStatus: row.LastProbeStatus, LastProbeErrorKind: row.LastProbeErrorKind,
		RetiredAt: timePointer(row.RetiredAt), CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(), ModelBindings: bindings,
	}, nil
}

func (r *RegistryRepository) ListCredentials(ctx context.Context, includeRetired bool) ([]registry.Credential, error) {
	rows, err := r.queries.ListCredentials(ctx, includeRetired)
	if err != nil {
		return nil, translateRegistryError(err)
	}
	items := make([]registry.Credential, 0, len(rows))
	for _, row := range rows {
		bindings, err := credentialBindings(ctx, r.queries, row.ID)
		if err != nil {
			return nil, err
		}
		item := registry.Credential{
			ID: row.ID, ResourcePoolID: row.ResourcePoolID, ResourcePoolName: row.ResourcePoolName, ResourcePoolSlug: row.ResourcePoolSlug,
			ProviderID: row.ProviderID, ProviderName: row.ProviderName, ProviderKind: providers.Kind(row.ProviderKind), ProviderBaseURL: row.ProviderBaseUrl,
			Name: row.Name, Status: registry.CredentialStatus(row.Status), RPMLimit: row.RpmLimit, TPMLimit: row.TpmLimit, ConcurrencyLimit: row.ConcurrencyLimit,
			CooldownUntil: timePointer(row.CooldownUntil), ConsecutiveFailures: row.ConsecutiveFailures, LastSuccessAt: timePointer(row.LastSuccessAt), LastErrorKind: row.LastErrorKind,
			LastProbeAt: timePointer(row.LastProbeAt), LastProbeLatencyMs: row.LastProbeLatencyMs, LastProbeKind: row.LastProbeKind, LastProbeStatus: row.LastProbeStatus, LastProbeErrorKind: row.LastProbeErrorKind,
			RetiredAt: timePointer(row.RetiredAt), CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(), ModelBindings: bindings,
		}
		if row.LastCheckedUnixSeconds >= 0 {
			checked := time.Unix(row.LastCheckedUnixSeconds, 0).UTC()
			item.LastCheckedAt = &checked
		}
		if row.TerminalCount > 0 {
			rate := float64(row.CompletedCount) / float64(row.TerminalCount)
			item.RecentSuccessRate = &rate
		}
		if row.FirstByteP95Ms >= 0 {
			item.FirstByteP95Ms = &row.FirstByteP95Ms
		}
		if row.TotalLatencyP95Ms >= 0 {
			item.TotalLatencyP95Ms = &row.TotalLatencyP95Ms
		}
		items = append(items, item)
	}
	return items, nil
}

func credentialBindings(ctx context.Context, queries *db.Queries, id uuid.UUID) ([]registry.CredentialModelBinding, error) {
	rows, err := queries.ListCredentialModelBindings(ctx, id)
	if err != nil {
		return nil, translateRegistryError(err)
	}
	items := make([]registry.CredentialModelBinding, 0, len(rows))
	for _, row := range rows {
		items = append(items, registry.CredentialModelBinding{ModelID: row.ModelID, ModelName: row.ModelName, Priority: row.Priority, Weight: row.Weight})
	}
	return items, nil
}

func (r *RegistryRepository) GetEncryptedCredential(ctx context.Context, id uuid.UUID) ([]byte, error) {
	value, err := r.queries.GetEncryptedCredential(ctx, id)
	if err != nil {
		return nil, translateRegistryError(err)
	}
	return value, nil
}

func (r *RegistryRepository) RecordCredentialProbe(ctx context.Context, id uuid.UUID, checkedAt time.Time, execution registry.CredentialProbeExecution, actorID uuid.UUID, requestID string) (registry.Credential, error) {
	tx, err := r.connections.Postgres.Begin(ctx)
	if err != nil {
		return registry.Credential{}, err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	latency := execution.LatencyMillis
	if _, err := queries.RecordCredentialProbe(ctx, db.RecordCredentialProbeParams{
		LastProbeAt: timestamp(checkedAt), LastProbeLatencyMs: &latency, LastProbeKind: &execution.Kind,
		LastProbeStatus: &execution.Status, LastProbeErrorKind: execution.ErrorKind, ID: id,
	}); err != nil {
		return registry.Credential{}, translateRegistryError(err)
	}
	audit := auditParams(&actorID, "credential.probed", "provider_credential", id.String(), map[string]any{
		"status": execution.Status, "error_kind": execution.ErrorKind, "retryable": execution.Retryable,
		"latency_ms": execution.LatencyMillis, "model_id": execution.ModelID,
	})
	audit.RequestID = &requestID
	if _, err := queries.CreateAuditEvent(ctx, audit); err != nil {
		return registry.Credential{}, err
	}
	credential, err := credentialByID(ctx, queries, id)
	if err != nil {
		return registry.Credential{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return registry.Credential{}, translateRegistryError(err)
	}
	return credential, nil
}
