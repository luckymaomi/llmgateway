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

func (r *RegistryRepository) ReplayCredentialMutation(ctx context.Context, actorID uuid.UUID, mutation registry.CredentialMutation) (registry.Credential, bool, error) {
	operation, err := r.queries.GetCredentialMutation(ctx, credentialMutationLookup(actorID, mutation))
	if errors.Is(err, pgx.ErrNoRows) {
		return registry.Credential{}, false, nil
	}
	if err != nil {
		return registry.Credential{}, false, translateRegistryError(err)
	}
	credential, err := credentialMutationResult(operation, mutation)
	return credential, true, err
}

func (r *RegistryRepository) CreateCredential(ctx context.Context, input registry.NewCredential, actorID uuid.UUID, mutation registry.CredentialMutation) (registry.Credential, error) {
	return r.executeCredentialMutation(ctx, actorID, mutation, func(queries *db.Queries) (registry.Credential, error) {
		modelNames, err := validateCredentialBindings(ctx, queries, input.ProviderID, input.ResourceDomain, input.AuthorizedModelIDs)
		if err != nil {
			return registry.Credential{}, err
		}
		created, err := queries.CreateCredential(ctx, db.CreateCredentialParams{
			ID: input.ID, ProviderID: input.ProviderID, Name: input.Name, EncryptedSecret: input.EncryptedSecret, ResourceDomain: db.ResourceDomain(input.ResourceDomain),
			RpmLimit: input.RPMLimit, TpmLimit: input.TPMLimit, ConcurrencyLimit: input.ConcurrencyLimit,
		})
		if err != nil {
			return registry.Credential{}, translateRegistryError(err)
		}
		if err := replaceCredentialBindings(ctx, queries, created.ID, input.AuthorizedModelIDs); err != nil {
			return registry.Credential{}, err
		}
		result := credentialWithModels(created, input.AuthorizedModelIDs, modelNames)
		params := auditParams(&actorID, "credential.created", "credential", created.ID.String(), credentialAuditDetail(nil, &created, nil, input.AuthorizedModelIDs))
		params.RequestID = &mutation.RequestID
		if _, err := queries.CreateAuditEvent(ctx, params); err != nil {
			return registry.Credential{}, err
		}
		return result, nil
	})
}

func (r *RegistryRepository) UpdateCredential(ctx context.Context, input registry.CredentialChange, actorID uuid.UUID, mutation registry.CredentialMutation) (registry.Credential, error) {
	return r.executeCredentialMutation(ctx, actorID, mutation, func(queries *db.Queries) (registry.Credential, error) {
		current, err := queries.GetCredentialForUpdate(ctx, input.ID)
		if err != nil {
			return registry.Credential{}, translateRegistryError(err)
		}
		if !current.UpdatedAt.Time.Equal(input.ExpectedUpdatedAt) {
			return registry.Credential{}, registry.ErrConflict
		}
		modelNames, err := validateCredentialBindings(ctx, queries, current.ProviderID, input.ResourceDomain, input.AuthorizedModelIDs)
		if err != nil {
			return registry.Credential{}, err
		}
		updated, err := queries.UpdateCredential(ctx, db.UpdateCredentialParams{
			ID: input.ID, Name: input.Name, ReplaceSecret: input.ReplaceSecret, EncryptedSecret: input.EncryptedSecret,
			ResourceDomain: db.ResourceDomain(input.ResourceDomain), RpmLimit: input.RPMLimit, TpmLimit: input.TPMLimit,
			ConcurrencyLimit: input.ConcurrencyLimit, ExpectedUpdatedAt: optionalTimestamp(&input.ExpectedUpdatedAt),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return registry.Credential{}, registry.ErrConflict
		}
		if err != nil {
			return registry.Credential{}, translateRegistryError(err)
		}
		beforeBindings, err := queries.ListCredentialModelBindingsForCredential(ctx, input.ID)
		if err != nil {
			return registry.Credential{}, err
		}
		beforeModelIDs := bindingModelIDs(beforeBindings)
		if err := replaceCredentialBindings(ctx, queries, input.ID, input.AuthorizedModelIDs); err != nil {
			return registry.Credential{}, err
		}
		result := credentialWithModels(updated, input.AuthorizedModelIDs, modelNames)
		params := auditParams(&actorID, "credential.updated", "credential", input.ID.String(), credentialAuditDetail(&current, &updated, beforeModelIDs, input.AuthorizedModelIDs))
		params.RequestID = &mutation.RequestID
		if _, err := queries.CreateAuditEvent(ctx, params); err != nil {
			return registry.Credential{}, err
		}
		return result, nil
	})
}

func (r *RegistryRepository) SetCredentialStatus(ctx context.Context, credentialID uuid.UUID, status registry.CredentialStatus, expectedUpdatedAt time.Time, actorID uuid.UUID, mutation registry.CredentialMutation) (registry.Credential, error) {
	return r.executeCredentialMutation(ctx, actorID, mutation, func(queries *db.Queries) (registry.Credential, error) {
		current, err := queries.GetCredentialForUpdate(ctx, credentialID)
		if err != nil {
			return registry.Credential{}, translateRegistryError(err)
		}
		if !current.UpdatedAt.Time.Equal(expectedUpdatedAt) {
			return registry.Credential{}, registry.ErrConflict
		}
		bindings, err := queries.ListCredentialModelBindingsForCredential(ctx, credentialID)
		if err != nil {
			return registry.Credential{}, err
		}
		updated, err := queries.SetCredentialStatus(ctx, db.SetCredentialStatusParams{ID: credentialID, Status: db.CredentialStatus(status), ExpectedUpdatedAt: optionalTimestamp(&expectedUpdatedAt)})
		if errors.Is(err, pgx.ErrNoRows) {
			return registry.Credential{}, registry.ErrConflict
		}
		if err != nil {
			return registry.Credential{}, translateRegistryError(err)
		}
		modelIDs, modelNames := bindingFacts(bindings)
		result := credentialWithModels(updated, modelIDs, modelNames)
		params := auditParams(&actorID, "credential.status_changed", "credential", credentialID.String(), credentialAuditDetail(&current, &updated, modelIDs, modelIDs))
		params.RequestID = &mutation.RequestID
		if _, err := queries.CreateAuditEvent(ctx, params); err != nil {
			return registry.Credential{}, err
		}
		return result, nil
	})
}

func (r *RegistryRepository) executeCredentialMutation(ctx context.Context, actorID uuid.UUID, mutation registry.CredentialMutation, apply func(*db.Queries) (registry.Credential, error)) (registry.Credential, error) {
	tx, err := r.connections.Postgres.Begin(ctx)
	if err != nil {
		return registry.Credential{}, err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	operation, err := queries.ClaimCredentialMutation(ctx, db.ClaimCredentialMutationParams{
		ActorUserID: actorID, Action: string(mutation.Action), IdempotencyKey: mutation.IdempotencyKey,
		RequestFingerprint: mutation.RequestFingerprint, RequestID: mutation.RequestID,
	})
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
	encoded, err := json.Marshal(result)
	if err != nil {
		return registry.Credential{}, fmt.Errorf("encode credential mutation result: %w", err)
	}
	credentialID := result.ID
	if _, err := queries.CompleteCredentialMutation(ctx, db.CompleteCredentialMutationParams{CredentialID: &credentialID, Result: encoded, ID: operation.ID}); err != nil {
		return registry.Credential{}, err
	}
	if err := r.commitCredentialMutation(ctx, tx); err != nil {
		return r.reconcileCredentialMutation(ctx, actorID, mutation, err)
	}
	return result, nil
}

func (r *RegistryRepository) reconcileCredentialMutation(ctx context.Context, actorID uuid.UUID, mutation registry.CredentialMutation, commitErr error) (registry.Credential, error) {
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	delay := 20 * time.Millisecond
	var reconciliationErr error
	for {
		operation, err := r.queries.GetCredentialMutation(reconcileCtx, credentialMutationLookup(actorID, mutation))
		if err == nil {
			return credentialMutationResult(operation, mutation)
		}
		reconciliationErr = err
		timer := time.NewTimer(delay)
		select {
		case <-reconcileCtx.Done():
			timer.Stop()
			return registry.Credential{}, fmt.Errorf("%w: commit: %v; reconciliation: %v", registry.ErrOutcomeUnknown, commitErr, reconciliationErr)
		case <-timer.C:
		}
		if delay < 250*time.Millisecond {
			delay *= 2
		}
	}
}

func (r *RegistryRepository) ListCredentials(ctx context.Context) ([]registry.Credential, error) {
	items, err := r.queries.ListCredentials(ctx)
	if err != nil {
		return nil, err
	}
	bindings, err := r.queries.ListCredentialModelBindings(ctx)
	if err != nil {
		return nil, err
	}
	modelIDs := make(map[uuid.UUID][]uuid.UUID)
	modelNames := make(map[uuid.UUID][]string)
	for _, binding := range bindings {
		modelIDs[binding.CredentialID] = append(modelIDs[binding.CredentialID], binding.ModelID)
		modelNames[binding.CredentialID] = append(modelNames[binding.CredentialID], binding.PublicName)
	}
	result := make([]registry.Credential, 0, len(items))
	for _, item := range items {
		credential := registry.Credential{
			ID: item.ID, ProviderID: item.ProviderID, Name: item.Name, ResourceDomain: registry.ResourceDomain(item.ResourceDomain),
			Status: registry.CredentialStatus(item.Status), RPMLimit: item.RpmLimit, TPMLimit: item.TpmLimit,
			ConcurrencyLimit: item.ConcurrencyLimit, CooldownUntil: timePointer(item.CooldownUntil),
			ConsecutiveFailures: item.ConsecutiveFailures, LastSuccessAt: timePointer(item.LastSuccessAt), LastErrorKind: item.LastErrorKind,
			LastProbeAt: timePointer(item.LastProbeAt), LastProbeLatencyMs: item.LastProbeLatencyMs, LastProbeKind: item.LastProbeKind,
			LastProbeStatus: item.LastProbeStatus, LastProbeErrorKind: item.LastProbeErrorKind,
			CreatedAt: item.CreatedAt.Time, UpdatedAt: item.UpdatedAt.Time,
		}
		credential.AuthorizedModelIDs = modelIDs[item.ID]
		credential.AuthorizedModels = modelNames[item.ID]
		result = append(result, credential)
	}
	return result, nil
}

func (r *RegistryRepository) GetCredential(ctx context.Context, id uuid.UUID) (registry.Credential, error) {
	item, err := r.queries.GetCredentialSecret(ctx, id)
	if err != nil {
		return registry.Credential{}, translateRegistryError(err)
	}
	bindings, err := r.queries.ListCredentialModelBindingsForCredential(ctx, id)
	if err != nil {
		return registry.Credential{}, err
	}
	modelIDs, modelNames := bindingFacts(bindings)
	return credentialWithModels(item, modelIDs, modelNames), nil
}

func (r *RegistryRepository) GetEncryptedCredential(ctx context.Context, id uuid.UUID) ([]byte, error) {
	credential, err := r.queries.GetCredentialSecret(ctx, id)
	if err != nil {
		return nil, translateRegistryError(err)
	}
	return append([]byte(nil), credential.EncryptedSecret...), nil
}

func (r *RegistryRepository) RecordCredentialProbe(ctx context.Context, id uuid.UUID, checkedAt time.Time, execution registry.CredentialProbeExecution, actorID uuid.UUID, requestID string) (registry.Credential, error) {
	tx, err := r.connections.Postgres.Begin(ctx)
	if err != nil {
		return registry.Credential{}, err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	latencyMillis := execution.LatencyMillis
	probeKind := execution.Kind
	probeStatus := execution.Status
	updated, err := queries.RecordCredentialProbe(ctx, db.RecordCredentialProbeParams{
		ID: id, LastProbeAt: optionalTimestamp(&checkedAt), LastProbeLatencyMs: &latencyMillis,
		LastProbeKind: &probeKind, LastProbeStatus: &probeStatus, LastProbeErrorKind: execution.ErrorKind,
	})
	if err != nil {
		return registry.Credential{}, translateRegistryError(err)
	}
	params := auditParams(&actorID, "credential.probed", "credential", id.String(), map[string]any{
		"kind": execution.Kind, "status": execution.Status, "latency_ms": execution.LatencyMillis,
		"error_kind": execution.ErrorKind, "may_use_tokens": execution.MayUseTokens,
	})
	params.RequestID = &requestID
	if _, err := queries.CreateAuditEvent(ctx, params); err != nil {
		return registry.Credential{}, err
	}
	bindings, err := queries.ListCredentialModelBindingsForCredential(ctx, id)
	if err != nil {
		return registry.Credential{}, err
	}
	modelIDs, modelNames := bindingFacts(bindings)
	result := credentialWithModels(updated, modelIDs, modelNames)
	if err := tx.Commit(ctx); err != nil {
		return registry.Credential{}, fmt.Errorf("%w: persist credential probe result", registry.ErrOutcomeUnknown)
	}
	return result, nil
}

func (r *RegistryRepository) BindCredentialModel(ctx context.Context, credentialID, modelID uuid.UUID, priority, weight int32, actorID uuid.UUID) error {
	if err := r.queries.BindCredentialModel(ctx, db.BindCredentialModelParams{CredentialID: credentialID, ModelID: modelID, Priority: priority, Weight: weight}); err != nil {
		return translateRegistryError(err)
	}
	_, err := r.queries.CreateAuditEvent(ctx, auditParams(&actorID, "credential.model_bound", "credential", credentialID.String(), map[string]any{"model_id": modelID, "priority": priority, "weight": weight}))
	return err
}

func credentialMutationLookup(actorID uuid.UUID, mutation registry.CredentialMutation) db.GetCredentialMutationParams {
	return db.GetCredentialMutationParams{ActorUserID: actorID, Action: string(mutation.Action), IdempotencyKey: mutation.IdempotencyKey}
}

func credentialMutationResult(operation db.CredentialMutation, mutation registry.CredentialMutation) (registry.Credential, error) {
	if operation.Action != string(mutation.Action) || !bytes.Equal(operation.RequestFingerprint, mutation.RequestFingerprint) {
		return registry.Credential{}, registry.ErrIdempotencyConflict
	}
	var result registry.Credential
	if err := json.Unmarshal(operation.Result, &result); err != nil || result.ID == uuid.Nil || operation.CredentialID == nil || *operation.CredentialID != result.ID {
		return registry.Credential{}, fmt.Errorf("registry store: invalid credential mutation result")
	}
	return result, nil
}

func credentialFromDB(credential db.ProviderCredential) registry.Credential {
	return registry.Credential{ID: credential.ID, ProviderID: credential.ProviderID, Name: credential.Name, ResourceDomain: registry.ResourceDomain(credential.ResourceDomain), Status: registry.CredentialStatus(credential.Status), RPMLimit: credential.RpmLimit, TPMLimit: credential.TpmLimit, ConcurrencyLimit: credential.ConcurrencyLimit, CooldownUntil: timePointer(credential.CooldownUntil), ConsecutiveFailures: credential.ConsecutiveFailures, LastSuccessAt: timePointer(credential.LastSuccessAt), LastErrorKind: credential.LastErrorKind, LastProbeAt: timePointer(credential.LastProbeAt), LastProbeLatencyMs: credential.LastProbeLatencyMs, LastProbeKind: credential.LastProbeKind, LastProbeStatus: credential.LastProbeStatus, LastProbeErrorKind: credential.LastProbeErrorKind, CreatedAt: credential.CreatedAt.Time, UpdatedAt: credential.UpdatedAt.Time}
}

func validateCredentialBindings(ctx context.Context, queries *db.Queries, providerID uuid.UUID, domain registry.ResourceDomain, modelIDs []uuid.UUID) ([]string, error) {
	modelNames := make([]string, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		model, err := queries.GetModelForCredentialBinding(ctx, modelID)
		if err != nil {
			return nil, translateRegistryError(err)
		}
		if model.ProviderID != providerID || registry.ResourceDomain(model.ResourceDomain) != domain {
			return nil, registry.ErrConflict
		}
		modelNames = append(modelNames, model.PublicName)
	}
	return modelNames, nil
}

func replaceCredentialBindings(ctx context.Context, queries *db.Queries, credentialID uuid.UUID, modelIDs []uuid.UUID) error {
	if err := queries.DeleteCredentialModelBindings(ctx, credentialID); err != nil {
		return translateRegistryError(err)
	}
	for _, modelID := range modelIDs {
		if err := queries.BindCredentialModel(ctx, db.BindCredentialModelParams{CredentialID: credentialID, ModelID: modelID, Priority: 100, Weight: 100}); err != nil {
			return translateRegistryError(err)
		}
	}
	return nil
}

func bindingFacts(bindings []db.ListCredentialModelBindingsForCredentialRow) ([]uuid.UUID, []string) {
	modelIDs := make([]uuid.UUID, 0, len(bindings))
	modelNames := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		modelIDs = append(modelIDs, binding.ModelID)
		modelNames = append(modelNames, binding.PublicName)
	}
	return modelIDs, modelNames
}

func bindingModelIDs(bindings []db.ListCredentialModelBindingsForCredentialRow) []uuid.UUID {
	modelIDs, _ := bindingFacts(bindings)
	return modelIDs
}

func credentialWithModels(credential db.ProviderCredential, modelIDs []uuid.UUID, modelNames []string) registry.Credential {
	result := credentialFromDB(credential)
	result.AuthorizedModelIDs = append([]uuid.UUID(nil), modelIDs...)
	result.AuthorizedModels = append([]string(nil), modelNames...)
	return result
}

func credentialAuditDetail(before, after *db.ProviderCredential, beforeModelIDs, afterModelIDs []uuid.UUID) map[string]any {
	detail := map[string]any{"before": nil, "after": nil}
	if before != nil {
		detail["before"] = credentialAuditSummary(*before, beforeModelIDs)
	}
	if after != nil {
		detail["after"] = credentialAuditSummary(*after, afterModelIDs)
	}
	return detail
}

func credentialAuditSummary(credential db.ProviderCredential, modelIDs []uuid.UUID) map[string]any {
	return map[string]any{
		"name": credential.Name, "provider_id": credential.ProviderID, "resource_domain": credential.ResourceDomain,
		"status": credential.Status, "rpm_limit": credential.RpmLimit, "tpm_limit": credential.TpmLimit,
		"concurrency_limit": credential.ConcurrencyLimit,
		"model_ids":         modelIDs,
	}
}
