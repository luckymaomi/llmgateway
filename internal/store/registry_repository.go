package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/luckymaomi/llmgateway/internal/registry"
	db "github.com/luckymaomi/llmgateway/internal/store/db"
)

func (r *RegistryRepository) CreateModel(ctx context.Context, input registry.Model, actorID uuid.UUID) (registry.Model, error) {
	capabilities, err := json.Marshal(input.Capabilities)
	if err != nil {
		return registry.Model{}, err
	}
	created, err := r.queries.CreateModel(ctx, db.CreateModelParams{ProviderID: input.ProviderID, PublicName: input.PublicName, UpstreamName: input.UpstreamName, DisplayName: input.DisplayName, ResourceDomain: db.ResourceDomain(input.ResourceDomain), Capabilities: capabilities, Enabled: input.Enabled})
	if err != nil {
		return registry.Model{}, translateRegistryError(err)
	}
	if _, err := r.queries.CreateAuditEvent(ctx, auditParams(&actorID, "model.created", "model", created.ID.String(), map[string]any{"public_name": created.PublicName})); err != nil {
		return registry.Model{}, err
	}
	return modelFromDB(created, "", "")
}

func (r *RegistryRepository) UpdateModel(ctx context.Context, input registry.Model, actorID uuid.UUID) (registry.Model, error) {
	capabilities, err := json.Marshal(input.Capabilities)
	if err != nil {
		return registry.Model{}, err
	}
	updated, err := r.queries.UpdateModel(ctx, db.UpdateModelParams{ID: input.ID, PublicName: input.PublicName, UpstreamName: input.UpstreamName, DisplayName: input.DisplayName, ResourceDomain: db.ResourceDomain(input.ResourceDomain), Capabilities: capabilities, Enabled: input.Enabled})
	if err != nil {
		return registry.Model{}, translateRegistryError(err)
	}
	if _, err := r.queries.CreateAuditEvent(ctx, auditParams(&actorID, "model.updated", "model", updated.ID.String(), map[string]any{"enabled": updated.Enabled})); err != nil {
		return registry.Model{}, err
	}
	return modelFromDB(updated, "", "")
}

func (r *RegistryRepository) ListModels(ctx context.Context) ([]registry.Model, error) {
	items, err := r.queries.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]registry.Model, 0, len(items))
	for _, item := range items {
		var capabilities registry.ModelCapabilities
		if err := json.Unmarshal(item.Capabilities, &capabilities); err != nil {
			return nil, fmt.Errorf("decode model capabilities: %w", err)
		}
		result = append(result, registry.Model{ID: item.ID, ProviderID: item.ProviderID, ProviderSlug: item.ProviderSlug, ProviderName: item.ProviderName, PublicName: item.PublicName, UpstreamName: item.UpstreamName, DisplayName: item.DisplayName, ResourceDomain: registry.ResourceDomain(item.ResourceDomain), Capabilities: capabilities, Enabled: item.Enabled, CreatedAt: item.CreatedAt.Time, UpdatedAt: item.UpdatedAt.Time})
	}
	return result, nil
}

func (r *RegistryRepository) CreateCredential(ctx context.Context, input registry.NewCredential, actorID uuid.UUID) (registry.Credential, error) {
	created, err := r.queries.CreateCredential(ctx, db.CreateCredentialParams{ID: input.ID, ProviderID: input.ProviderID, Name: input.Name, EncryptedSecret: input.EncryptedSecret, ResourceDomain: db.ResourceDomain(input.ResourceDomain), RpmLimit: input.RPMLimit, TpmLimit: input.TPMLimit, ConcurrencyLimit: input.ConcurrencyLimit, FixedProxyUrl: input.FixedProxyURL})
	if err != nil {
		return registry.Credential{}, translateRegistryError(err)
	}
	if _, err := r.queries.CreateAuditEvent(ctx, auditParams(&actorID, "credential.created", "credential", created.ID.String(), map[string]any{"provider_id": created.ProviderID, "resource_domain": created.ResourceDomain})); err != nil {
		return registry.Credential{}, err
	}
	return credentialFromDB(created), nil
}

func (r *RegistryRepository) ListCredentials(ctx context.Context) ([]registry.Credential, error) {
	items, err := r.queries.ListCredentials(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]registry.Credential, 0, len(items))
	for _, item := range items {
		result = append(result, registry.Credential{ID: item.ID, ProviderID: item.ProviderID, Name: item.Name, ResourceDomain: registry.ResourceDomain(item.ResourceDomain), Status: registry.CredentialStatus(item.Status), RPMLimit: item.RpmLimit, TPMLimit: item.TpmLimit, ConcurrencyLimit: item.ConcurrencyLimit, FixedProxyURL: item.FixedProxyUrl, CooldownUntil: timePointer(item.CooldownUntil), ConsecutiveFailures: item.ConsecutiveFailures, LastSuccessAt: timePointer(item.LastSuccessAt), LastErrorKind: item.LastErrorKind, CreatedAt: item.CreatedAt.Time, UpdatedAt: item.UpdatedAt.Time})
	}
	return result, nil
}

func (r *RegistryRepository) GetEncryptedCredential(ctx context.Context, id uuid.UUID) ([]byte, error) {
	credential, err := r.queries.GetCredentialSecret(ctx, id)
	if err != nil {
		return nil, translateRegistryError(err)
	}
	return append([]byte(nil), credential.EncryptedSecret...), nil
}

func (r *RegistryRepository) BindCredentialModel(ctx context.Context, credentialID, modelID uuid.UUID, priority, weight int32, actorID uuid.UUID) error {
	if err := r.queries.BindCredentialModel(ctx, db.BindCredentialModelParams{CredentialID: credentialID, ModelID: modelID, Priority: priority, Weight: weight}); err != nil {
		return translateRegistryError(err)
	}
	_, err := r.queries.CreateAuditEvent(ctx, auditParams(&actorID, "credential.model_bound", "credential", credentialID.String(), map[string]any{"model_id": modelID, "priority": priority, "weight": weight}))
	return err
}

func modelFromDB(model db.Model, providerSlug, providerName string) (registry.Model, error) {
	var capabilities registry.ModelCapabilities
	if err := json.Unmarshal(model.Capabilities, &capabilities); err != nil {
		return registry.Model{}, fmt.Errorf("decode model capabilities: %w", err)
	}
	return registry.Model{ID: model.ID, ProviderID: model.ProviderID, ProviderSlug: providerSlug, ProviderName: providerName, PublicName: model.PublicName, UpstreamName: model.UpstreamName, DisplayName: model.DisplayName, ResourceDomain: registry.ResourceDomain(model.ResourceDomain), Capabilities: capabilities, Enabled: model.Enabled, CreatedAt: model.CreatedAt.Time, UpdatedAt: model.UpdatedAt.Time}, nil
}

func credentialFromDB(credential db.ProviderCredential) registry.Credential {
	return registry.Credential{ID: credential.ID, ProviderID: credential.ProviderID, Name: credential.Name, ResourceDomain: registry.ResourceDomain(credential.ResourceDomain), Status: registry.CredentialStatus(credential.Status), RPMLimit: credential.RpmLimit, TPMLimit: credential.TpmLimit, ConcurrencyLimit: credential.ConcurrencyLimit, FixedProxyURL: credential.FixedProxyUrl, CooldownUntil: timePointer(credential.CooldownUntil), ConsecutiveFailures: credential.ConsecutiveFailures, LastSuccessAt: timePointer(credential.LastSuccessAt), LastErrorKind: credential.LastErrorKind, CreatedAt: credential.CreatedAt.Time, UpdatedAt: credential.UpdatedAt.Time}
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
