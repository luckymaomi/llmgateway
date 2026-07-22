package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/luckymaomi/llmgateway/internal/siteprofile"
	db "github.com/luckymaomi/llmgateway/internal/store/db"
)

type SiteProfileRepository struct {
	connections *Connections
	queries     *db.Queries
}

func NewSiteProfileRepository(connections *Connections) *SiteProfileRepository {
	return &SiteProfileRepository{connections: connections, queries: db.New(connections.Postgres)}
}

func (r *SiteProfileRepository) Get(ctx context.Context) (siteprofile.Profile, error) {
	profile, err := r.queries.GetSiteProfile(ctx)
	if err != nil {
		return siteprofile.Profile{}, err
	}
	return siteProfileFromDB(profile), nil
}

func (r *SiteProfileRepository) Update(ctx context.Context, input siteprofile.Update) (siteprofile.Profile, error) {
	tx, err := r.connections.Postgres.Begin(ctx)
	if err != nil {
		return siteprofile.Profile{}, err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	before, err := queries.GetSiteProfile(ctx)
	if err != nil {
		return siteprofile.Profile{}, err
	}
	updated, err := queries.UpdateSiteProfile(ctx, db.UpdateSiteProfileParams{
		Name: input.Name, Description: input.Description, Contact: input.Contact,
		UpdatedBy: &input.ActorID, ExpectedVersion: input.ExpectedVersion,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return siteprofile.Profile{}, siteprofile.ErrConflict
	}
	if err != nil {
		return siteprofile.Profile{}, err
	}
	audit := auditParams(&input.ActorID, "site_profile.updated", "site_profile", "singleton", map[string]any{
		"before": map[string]any{"name": before.Name, "description": before.Description, "contact": before.Contact, "version": before.Version},
		"after":  map[string]any{"name": updated.Name, "description": updated.Description, "contact": updated.Contact, "version": updated.Version},
	})
	audit.RequestID = &input.RequestID
	if _, err := queries.CreateAuditEvent(ctx, audit); err != nil {
		return siteprofile.Profile{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return siteprofile.Profile{}, err
	}
	return siteProfileFromDB(updated), nil
}

func siteProfileFromDB(profile db.SiteProfile) siteprofile.Profile {
	return siteprofile.Profile{
		Name: profile.Name, Description: profile.Description, Contact: profile.Contact,
		Version: profile.Version, UpdatedAt: profile.UpdatedAt.Time.UTC(),
	}
}
