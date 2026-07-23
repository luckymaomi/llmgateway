package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/registry"
	db "github.com/luckymaomi/llmgateway/internal/store/db"
)

type RegistryRepository struct {
	connections                *Connections
	queries                    *db.Queries
	commitResourcePoolMutation func(context.Context, pgx.Tx) error
	commitCredentialMutation   func(context.Context, pgx.Tx) error
}

func NewRegistryRepository(connections *Connections) *RegistryRepository {
	return &RegistryRepository{
		connections:                connections,
		queries:                    db.New(connections.Postgres),
		commitResourcePoolMutation: func(ctx context.Context, tx pgx.Tx) error { return tx.Commit(ctx) },
		commitCredentialMutation:   func(ctx context.Context, tx pgx.Tx) error { return tx.Commit(ctx) },
	}
}

func (r *RegistryRepository) SyncCatalog(ctx context.Context, projections []registry.ProviderProjection) error {
	tx, err := r.connections.Postgres.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	for _, projection := range projections {
		provider, err := queries.UpsertProviderProjection(ctx, db.UpsertProviderProjectionParams{
			CatalogID: projection.CatalogID, Slug: projection.Slug, Name: projection.Name, Kind: string(projection.Kind),
			BaseUrl: projection.BaseURL, SourceUrl: projection.SourceURL, VerifiedAt: timestamp(projection.VerifiedAt),
		})
		if err != nil {
			return translateRegistryError(err)
		}
		for _, model := range projection.Models {
			capabilities, err := json.Marshal(model.Capabilities)
			if err != nil {
				return fmt.Errorf("encode catalog model %s: %w", model.PublicName, err)
			}
			if _, err := queries.UpsertModelProjection(ctx, db.UpsertModelProjectionParams{
				ProviderID: provider.ID, PublicName: model.PublicName, UpstreamName: model.UpstreamName,
				DisplayName: model.DisplayName, Capabilities: capabilities,
			}); err != nil {
				return translateRegistryError(err)
			}
		}
	}
	return tx.Commit(ctx)
}

func (r *RegistryRepository) ListProviders(ctx context.Context) ([]registry.Provider, error) {
	rows, err := r.queries.ListProviders(ctx)
	if err != nil {
		return nil, translateRegistryError(err)
	}
	items := make([]registry.Provider, 0, len(rows))
	for _, row := range rows {
		items = append(items, registry.Provider{
			ID: row.ID, CatalogID: row.CatalogID, Slug: row.Slug, Name: row.Name, Kind: providers.Kind(row.Kind),
			BaseURL: row.BaseUrl, SourceURL: row.SourceUrl, VerifiedAt: row.VerifiedAt.Time.UTC(),
			ResourcePoolCount: row.ResourcePoolCount, ActiveCredentialCount: row.ActiveCredentialCount,
			CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
		})
	}
	return items, nil
}

func (r *RegistryRepository) GetProvider(ctx context.Context, id uuid.UUID) (registry.Provider, error) {
	row, err := r.queries.GetProvider(ctx, id)
	if err != nil {
		return registry.Provider{}, translateRegistryError(err)
	}
	return providerFromDB(row), nil
}

func providerFromDB(row db.Provider) registry.Provider {
	return registry.Provider{
		ID: row.ID, CatalogID: row.CatalogID, Slug: row.Slug, Name: row.Name, Kind: providers.Kind(row.Kind),
		BaseURL: row.BaseUrl, SourceURL: row.SourceUrl, VerifiedAt: row.VerifiedAt.Time.UTC(),
		CreatedAt: row.CreatedAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
	}
}
