package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

func (r *ConfigurationRepository) ReplayRevisionMutation(ctx context.Context, actorID uuid.UUID, mutation configuration.Mutation) (configuration.Revision, bool, error) {
	operation, err := r.queries.GetConfigMutation(ctx, configMutationLookup(actorID, mutation))
	if errors.Is(err, pgx.ErrNoRows) {
		return configuration.Revision{}, false, nil
	}
	if err != nil {
		return configuration.Revision{}, false, translateConfigurationError(err)
	}
	revision, err := revisionMutationResult(operation, mutation)
	return revision, true, err
}

func (r *ConfigurationRepository) CreateRevision(ctx context.Context, actorID uuid.UUID, mutation configuration.Mutation) (configuration.Revision, error) {
	tx, err := r.connections.Postgres.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return configuration.Revision{}, err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	operation, err := queries.ClaimConfigMutation(ctx, claimConfigMutationParams(actorID, mutation))
	if errors.Is(err, pgx.ErrNoRows) {
		existing, loadErr := queries.GetConfigMutation(ctx, configMutationLookup(actorID, mutation))
		if loadErr != nil {
			return configuration.Revision{}, translateConfigurationError(loadErr)
		}
		return revisionMutationResult(existing, mutation)
	}
	if err != nil {
		return configuration.Revision{}, translateConfigurationError(err)
	}

	catalog, err := captureCatalog(ctx, queries)
	if err != nil {
		return configuration.Revision{}, err
	}
	_, checksum, err := encodeCatalog(catalog)
	if err != nil {
		return configuration.Revision{}, err
	}
	created, err := queries.CreateConfigRevision(ctx, db.CreateConfigRevisionParams{Checksum: checksum, CreatedBy: actorID})
	if err != nil {
		return configuration.Revision{}, translateConfigurationError(err)
	}
	if err := persistCatalog(ctx, queries, created.ID, catalog); err != nil {
		return configuration.Revision{}, err
	}
	revision, err := revisionFromDB(created, catalogSummary(catalog))
	if err != nil {
		return configuration.Revision{}, err
	}
	params := auditParams(&actorID, "configuration.revision_captured", "config_revision", created.ID.String(), map[string]any{
		"checksum": checksum, "catalog": revision.Catalog,
	})
	params.RequestID = &mutation.RequestID
	if _, err := queries.CreateAuditEvent(ctx, params); err != nil {
		return configuration.Revision{}, err
	}
	encodedResult, err := json.Marshal(revision)
	if err != nil {
		return configuration.Revision{}, fmt.Errorf("encode configuration mutation result: %w", err)
	}
	revisionID := revision.ID
	if _, err := queries.CompleteConfigMutation(ctx, db.CompleteConfigMutationParams{RevisionID: &revisionID, Result: encodedResult, ID: operation.ID}); err != nil {
		return configuration.Revision{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return r.reconcileRevisionMutation(ctx, actorID, mutation, err)
	}
	return revision, nil
}

func (r *ConfigurationRepository) GetRevision(ctx context.Context, id uuid.UUID) (configuration.Revision, error) {
	revision, err := r.queries.GetConfigRevision(ctx, id)
	if err != nil {
		return configuration.Revision{}, translateConfigurationError(err)
	}
	summary, err := r.queries.GetConfigRevisionCatalogSummary(ctx, id)
	if err != nil {
		return configuration.Revision{}, translateConfigurationError(err)
	}
	return revisionFromDB(revision, catalogSummaryFromDB(summary))
}

func (r *ConfigurationRepository) ListRevisions(ctx context.Context, offset, size int32) ([]configuration.Revision, error) {
	items, err := r.queries.ListConfigRevisions(ctx, db.ListConfigRevisionsParams{PageOffset: offset, PageSize: size})
	if err != nil {
		return nil, err
	}
	result := make([]configuration.Revision, 0, len(items))
	for _, item := range items {
		summary, err := r.queries.GetConfigRevisionCatalogSummary(ctx, item.ID)
		if err != nil {
			return nil, translateConfigurationError(err)
		}
		revision, err := revisionFromDB(item, catalogSummaryFromDB(summary))
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
	summary, err := r.queries.GetConfigRevisionCatalogSummary(ctx, active.ID)
	if err != nil {
		return configuration.Active{}, translateConfigurationError(err)
	}
	return activeFromDB(active, catalogSummaryFromDB(summary))
}

func (r *ConfigurationRepository) ActiveCatalog(ctx context.Context) (configuration.Active, configuration.Catalog, error) {
	activeRow, err := r.queries.GetActiveConfig(ctx)
	if err != nil {
		return configuration.Active{}, configuration.Catalog{}, translateConfigurationError(err)
	}
	catalog, err := loadCatalog(ctx, r.queries, activeRow.ID)
	if err != nil {
		return configuration.Active{}, configuration.Catalog{}, translateConfigurationError(err)
	}
	active, err := activeFromDB(activeRow, catalogSummary(catalog))
	if err != nil {
		return configuration.Active{}, configuration.Catalog{}, err
	}
	_, checksum, err := encodeCatalog(catalog)
	if err != nil {
		return configuration.Active{}, configuration.Catalog{}, err
	}
	if checksum != active.Revision.Checksum {
		return configuration.Active{}, configuration.Catalog{}, fmt.Errorf("configuration snapshot checksum mismatch for active revision %s", active.Revision.ID)
	}
	return active, catalog, nil
}

func (r *ConfigurationRepository) ReplayPublishMutation(ctx context.Context, actorID uuid.UUID, mutation configuration.Mutation) (configuration.Active, bool, error) {
	operation, err := r.queries.GetConfigMutation(ctx, configMutationLookup(actorID, mutation))
	if errors.Is(err, pgx.ErrNoRows) {
		return configuration.Active{}, false, nil
	}
	if err != nil {
		return configuration.Active{}, false, translateConfigurationError(err)
	}
	active, err := publishMutationResult(operation, mutation)
	return active, true, err
}

func (r *ConfigurationRepository) Publish(ctx context.Context, revisionID uuid.UUID, expectedVersion int64, actorID uuid.UUID, mutation configuration.Mutation) (configuration.Active, error) {
	tx, err := r.connections.Postgres.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return configuration.Active{}, err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	operation, err := queries.ClaimConfigMutation(ctx, claimConfigMutationParams(actorID, mutation))
	if errors.Is(err, pgx.ErrNoRows) {
		existing, loadErr := queries.GetConfigMutation(ctx, configMutationLookup(actorID, mutation))
		if loadErr != nil {
			return configuration.Active{}, translateConfigurationError(loadErr)
		}
		return publishMutationResult(existing, mutation)
	}
	if err != nil {
		return configuration.Active{}, translateConfigurationError(err)
	}
	revision, err := queries.GetConfigRevision(ctx, revisionID)
	if err != nil {
		return configuration.Active{}, translateConfigurationError(err)
	}
	catalog, err := loadCatalog(ctx, queries, revisionID)
	if err != nil {
		return configuration.Active{}, err
	}
	if !publishableCatalog(catalog) {
		return configuration.Active{}, configuration.ErrInvalidInput
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
		return configuration.Active{}, translateConfigurationError(lockErr)
	default:
		if active.Version != expectedVersion {
			return configuration.Active{}, configuration.ErrConflict
		}
		if active.RevisionID == revisionID {
			activeRow, err := queries.GetActiveConfig(ctx)
			if err != nil {
				return configuration.Active{}, translateConfigurationError(err)
			}
			result, err := activeFromDB(activeRow, catalogSummary(catalog))
			if err != nil {
				return configuration.Active{}, err
			}
			if err := completePublishMutation(ctx, queries, operation.ID, result); err != nil {
				return configuration.Active{}, err
			}
			if err := tx.Commit(ctx); err != nil {
				return r.reconcilePublishMutation(ctx, actorID, mutation, err)
			}
			return result, nil
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
	_, checksum, err := encodeCatalog(catalog)
	if err != nil {
		return configuration.Active{}, err
	}
	if checksum != revision.Checksum {
		return configuration.Active{}, fmt.Errorf("configuration snapshot checksum mismatch for revision %s", revisionID)
	}
	auditAction := "configuration.published"
	if mutation.Action == configuration.MutationRollback {
		auditAction = "configuration.rolled_back"
	}
	params := auditParams(&actorID, auditAction, "config_revision", revisionID.String(), map[string]any{"active_version": activeVersion, "previous_version": expectedVersion})
	params.RequestID = &mutation.RequestID
	if _, err := queries.CreateAuditEvent(ctx, params); err != nil {
		return configuration.Active{}, err
	}
	activeRow, err := queries.GetActiveConfig(ctx)
	if err != nil {
		return configuration.Active{}, translateConfigurationError(err)
	}
	result, err := activeFromDB(activeRow, catalogSummary(catalog))
	if err != nil {
		return configuration.Active{}, err
	}
	if err := completePublishMutation(ctx, queries, operation.ID, result); err != nil {
		return configuration.Active{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return r.reconcilePublishMutation(ctx, actorID, mutation, err)
	}
	return result, nil
}

func completePublishMutation(ctx context.Context, queries *db.Queries, operationID uuid.UUID, result configuration.Active) error {
	encodedResult, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode configuration publish result: %w", err)
	}
	revisionID := result.Revision.ID
	_, err = queries.CompleteConfigMutation(ctx, db.CompleteConfigMutationParams{RevisionID: &revisionID, Result: encodedResult, ID: operationID})
	return err
}

func captureCatalog(ctx context.Context, queries *db.Queries) (configuration.Catalog, error) {
	providers, err := queries.ListRegistryProvidersForSnapshot(ctx)
	if err != nil {
		return configuration.Catalog{}, err
	}
	models, err := queries.ListRegistryModelsForSnapshot(ctx)
	if err != nil {
		return configuration.Catalog{}, err
	}
	credentials, err := queries.ListRegistryCredentialsForSnapshot(ctx)
	if err != nil {
		return configuration.Catalog{}, err
	}
	routes, err := queries.ListRegistryRoutesForSnapshot(ctx)
	if err != nil {
		return configuration.Catalog{}, err
	}
	catalog := configuration.Catalog{
		Providers: make([]configuration.CatalogProvider, 0, len(providers)), Models: make([]configuration.CatalogModel, 0, len(models)),
		Credentials: make([]configuration.CatalogCredential, 0, len(credentials)), Routes: make([]configuration.CatalogRoute, 0, len(routes)),
	}
	for _, item := range providers {
		catalog.Providers = append(catalog.Providers, configuration.CatalogProvider{ID: item.ID, Slug: item.Slug, Name: item.Name, Kind: item.Kind, BaseURL: item.BaseUrl})
	}
	for _, item := range models {
		catalog.Models = append(catalog.Models, configuration.CatalogModel{
			ID: item.ID, ProviderID: item.ProviderID, PublicName: item.PublicName, UpstreamName: item.UpstreamName, DisplayName: item.DisplayName,
			ResourceDomain: string(item.ResourceDomain), Capabilities: append(json.RawMessage(nil), item.Capabilities...), CreatedAt: item.CreatedAt.Time.UTC(),
		})
	}
	for _, item := range credentials {
		catalog.Credentials = append(catalog.Credentials, configuration.CatalogCredential{
			ID: item.ID, ProviderID: item.ProviderID, ResourceDomain: string(item.ResourceDomain), RPMLimit: item.RpmLimit, TPMLimit: item.TpmLimit,
			ConcurrencyLimit: item.ConcurrencyLimit,
		})
	}
	for _, item := range routes {
		catalog.Routes = append(catalog.Routes, configuration.CatalogRoute{ModelID: item.ModelID, CredentialID: item.CredentialID, Priority: item.Priority, Weight: item.Weight})
	}
	return catalog, nil
}

func persistCatalog(ctx context.Context, queries *db.Queries, revisionID uuid.UUID, catalog configuration.Catalog) error {
	for _, item := range catalog.Providers {
		if err := queries.CreateConfigRevisionProvider(ctx, db.CreateConfigRevisionProviderParams{RevisionID: revisionID, ProviderID: item.ID, Slug: item.Slug, Name: item.Name, Kind: item.Kind, BaseUrl: item.BaseURL}); err != nil {
			return translateConfigurationError(err)
		}
	}
	for _, item := range catalog.Models {
		if err := queries.CreateConfigRevisionModel(ctx, db.CreateConfigRevisionModelParams{
			RevisionID: revisionID, ModelID: item.ID, ProviderID: item.ProviderID, PublicName: item.PublicName, UpstreamName: item.UpstreamName,
			DisplayName: item.DisplayName, ResourceDomain: db.ResourceDomain(item.ResourceDomain), Capabilities: item.Capabilities, CreatedAt: timestamp(item.CreatedAt),
		}); err != nil {
			return translateConfigurationError(err)
		}
	}
	for _, item := range catalog.Credentials {
		if err := queries.CreateConfigRevisionCredential(ctx, db.CreateConfigRevisionCredentialParams{
			RevisionID: revisionID, CredentialID: item.ID, ProviderID: item.ProviderID, ResourceDomain: db.ResourceDomain(item.ResourceDomain),
			RpmLimit: item.RPMLimit, TpmLimit: item.TPMLimit, ConcurrencyLimit: item.ConcurrencyLimit,
		}); err != nil {
			return translateConfigurationError(err)
		}
	}
	for _, item := range catalog.Routes {
		if err := queries.CreateConfigRevisionRoute(ctx, db.CreateConfigRevisionRouteParams{RevisionID: revisionID, ModelID: item.ModelID, CredentialID: item.CredentialID, Priority: item.Priority, Weight: item.Weight}); err != nil {
			return translateConfigurationError(err)
		}
	}
	return nil
}

func loadCatalog(ctx context.Context, queries *db.Queries, revisionID uuid.UUID) (configuration.Catalog, error) {
	providers, err := queries.ListConfigRevisionProviders(ctx, revisionID)
	if err != nil {
		return configuration.Catalog{}, err
	}
	models, err := queries.ListConfigRevisionModels(ctx, revisionID)
	if err != nil {
		return configuration.Catalog{}, err
	}
	credentials, err := queries.ListConfigRevisionCredentials(ctx, revisionID)
	if err != nil {
		return configuration.Catalog{}, err
	}
	routes, err := queries.ListConfigRevisionRoutes(ctx, revisionID)
	if err != nil {
		return configuration.Catalog{}, err
	}
	catalog := configuration.Catalog{
		Providers: make([]configuration.CatalogProvider, 0, len(providers)), Models: make([]configuration.CatalogModel, 0, len(models)),
		Credentials: make([]configuration.CatalogCredential, 0, len(credentials)), Routes: make([]configuration.CatalogRoute, 0, len(routes)),
	}
	for _, item := range providers {
		catalog.Providers = append(catalog.Providers, configuration.CatalogProvider{ID: item.ProviderID, Slug: item.Slug, Name: item.Name, Kind: item.Kind, BaseURL: item.BaseUrl})
	}
	for _, item := range models {
		catalog.Models = append(catalog.Models, configuration.CatalogModel{
			ID: item.ModelID, ProviderID: item.ProviderID, PublicName: item.PublicName, UpstreamName: item.UpstreamName, DisplayName: item.DisplayName,
			ResourceDomain: string(item.ResourceDomain), Capabilities: append(json.RawMessage(nil), item.Capabilities...), CreatedAt: item.CreatedAt.Time.UTC(),
		})
	}
	for _, item := range credentials {
		catalog.Credentials = append(catalog.Credentials, configuration.CatalogCredential{
			ID: item.CredentialID, ProviderID: item.ProviderID, ResourceDomain: string(item.ResourceDomain), RPMLimit: item.RpmLimit, TPMLimit: item.TpmLimit,
			ConcurrencyLimit: item.ConcurrencyLimit,
		})
	}
	for _, item := range routes {
		catalog.Routes = append(catalog.Routes, configuration.CatalogRoute{ModelID: item.ModelID, CredentialID: item.CredentialID, Priority: item.Priority, Weight: item.Weight})
	}
	return catalog, nil
}

func encodeCatalog(catalog configuration.Catalog) ([]byte, string, error) {
	encoded, err := json.Marshal(catalog)
	if err != nil {
		return nil, "", fmt.Errorf("encode published configuration catalog: %w", err)
	}
	checksum := sha256.Sum256(encoded)
	return encoded, hex.EncodeToString(checksum[:]), nil
}

func catalogSummary(catalog configuration.Catalog) configuration.CatalogSummary {
	return configuration.CatalogSummary{ProviderCount: int64(len(catalog.Providers)), ModelCount: int64(len(catalog.Models)), CredentialCount: int64(len(catalog.Credentials)), RouteCount: int64(len(catalog.Routes))}
}

func publishableCatalog(catalog configuration.Catalog) bool {
	if len(catalog.Models) == 0 || len(catalog.Routes) == 0 {
		return false
	}
	routedModels := make(map[uuid.UUID]struct{}, len(catalog.Routes))
	for _, route := range catalog.Routes {
		routedModels[route.ModelID] = struct{}{}
	}
	for _, model := range catalog.Models {
		if _, exists := routedModels[model.ID]; !exists {
			return false
		}
	}
	return true
}

func catalogSummaryFromDB(row db.GetConfigRevisionCatalogSummaryRow) configuration.CatalogSummary {
	return configuration.CatalogSummary{ProviderCount: row.ProviderCount, ModelCount: row.ModelCount, CredentialCount: row.CredentialCount, RouteCount: row.RouteCount}
}

func revisionFromDB(revision db.ConfigRevision, summary configuration.CatalogSummary) (configuration.Revision, error) {
	if revision.Revision == nil {
		return configuration.Revision{}, fmt.Errorf("configuration revision identity is missing")
	}
	return configuration.Revision{ID: revision.ID, Revision: *revision.Revision, Checksum: revision.Checksum, Catalog: summary, CreatedBy: revision.CreatedBy, CreatedAt: revision.CreatedAt.Time.UTC(), PublishedAt: timePointer(revision.PublishedAt), PublishedBy: revision.PublishedBy}, nil
}

func activeFromDB(active db.GetActiveConfigRow, summary configuration.CatalogSummary) (configuration.Active, error) {
	if active.Revision == nil {
		return configuration.Active{}, fmt.Errorf("active configuration revision identity is missing")
	}
	return configuration.Active{Revision: configuration.Revision{ID: active.ID, Revision: *active.Revision, Checksum: active.Checksum, Catalog: summary, CreatedBy: active.CreatedBy, CreatedAt: active.CreatedAt.Time.UTC(), PublishedAt: timePointer(active.PublishedAt), PublishedBy: active.PublishedBy}, Version: active.ActiveVersion, UpdatedAt: active.ActiveUpdatedAt.Time.UTC()}, nil
}

func claimConfigMutationParams(actorID uuid.UUID, mutation configuration.Mutation) db.ClaimConfigMutationParams {
	return db.ClaimConfigMutationParams{ActorUserID: actorID, Action: string(mutation.Action), IdempotencyKey: mutation.IdempotencyKey, RequestFingerprint: mutation.RequestFingerprint, RequestID: mutation.RequestID}
}

func configMutationLookup(actorID uuid.UUID, mutation configuration.Mutation) db.GetConfigMutationParams {
	return db.GetConfigMutationParams{ActorUserID: actorID, Action: string(mutation.Action), IdempotencyKey: mutation.IdempotencyKey}
}

func revisionMutationResult(operation db.ConfigMutation, mutation configuration.Mutation) (configuration.Revision, error) {
	if !bytes.Equal(operation.RequestFingerprint, mutation.RequestFingerprint) {
		return configuration.Revision{}, configuration.ErrIdempotencyConflict
	}
	var result configuration.Revision
	if err := json.Unmarshal(operation.Result, &result); err != nil || result.ID == uuid.Nil || operation.RevisionID == nil || *operation.RevisionID != result.ID {
		return configuration.Revision{}, fmt.Errorf("configuration store: invalid revision mutation result")
	}
	return result, nil
}

func publishMutationResult(operation db.ConfigMutation, mutation configuration.Mutation) (configuration.Active, error) {
	if !bytes.Equal(operation.RequestFingerprint, mutation.RequestFingerprint) {
		return configuration.Active{}, configuration.ErrIdempotencyConflict
	}
	var result configuration.Active
	if err := json.Unmarshal(operation.Result, &result); err != nil || result.Revision.ID == uuid.Nil || operation.RevisionID == nil || *operation.RevisionID != result.Revision.ID || result.Version < 1 {
		return configuration.Active{}, fmt.Errorf("configuration store: invalid publish mutation result")
	}
	return result, nil
}

func (r *ConfigurationRepository) reconcileRevisionMutation(ctx context.Context, actorID uuid.UUID, mutation configuration.Mutation, commitErr error) (configuration.Revision, error) {
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	for delay := 20 * time.Millisecond; ; delay = minDuration(delay*2, 250*time.Millisecond) {
		operation, err := r.queries.GetConfigMutation(reconcileCtx, configMutationLookup(actorID, mutation))
		if err == nil {
			return revisionMutationResult(operation, mutation)
		}
		if !waitForReconcile(reconcileCtx, delay) {
			return configuration.Revision{}, fmt.Errorf("%w: commit: %v; reconciliation: %v", configuration.ErrOutcomeUnknown, commitErr, err)
		}
	}
}

func (r *ConfigurationRepository) reconcilePublishMutation(ctx context.Context, actorID uuid.UUID, mutation configuration.Mutation, commitErr error) (configuration.Active, error) {
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	for delay := 20 * time.Millisecond; ; delay = minDuration(delay*2, 250*time.Millisecond) {
		operation, err := r.queries.GetConfigMutation(reconcileCtx, configMutationLookup(actorID, mutation))
		if err == nil {
			return publishMutationResult(operation, mutation)
		}
		if !waitForReconcile(reconcileCtx, delay) {
			return configuration.Active{}, fmt.Errorf("%w: commit: %v; reconciliation: %v", configuration.ErrOutcomeUnknown, commitErr, err)
		}
	}
}

func waitForReconcile(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func minDuration(left, right time.Duration) time.Duration {
	if left < right {
		return left
	}
	return right
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
