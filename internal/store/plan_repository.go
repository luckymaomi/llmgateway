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
	db "github.com/luckymaomi/llmgateway/internal/store/db"
	"github.com/luckymaomi/llmgateway/internal/subscription"
)

type SubscriptionRepository struct {
	connections                *Connections
	queries                    *db.Queries
	commitPlanMutation         func(context.Context, pgx.Tx) error
	commitSubscriptionMutation func(context.Context, pgx.Tx) error
}

func NewSubscriptionRepository(connections *Connections) *SubscriptionRepository {
	return &SubscriptionRepository{
		connections: connections, queries: db.New(connections.Postgres),
		commitPlanMutation:         func(ctx context.Context, tx pgx.Tx) error { return tx.Commit(ctx) },
		commitSubscriptionMutation: func(ctx context.Context, tx pgx.Tx) error { return tx.Commit(ctx) },
	}
}

func (r *SubscriptionRepository) PublishPlan(ctx context.Context, draft subscription.PlanDraft, actorID uuid.UUID, mutation subscription.Mutation) (subscription.ServicePlan, error) {
	return r.executePlanMutation(ctx, actorID, mutation, func(queries *db.Queries) (subscription.ServicePlan, error) {
		planID := draft.ID
		if planID == uuid.Nil {
			created, err := queries.CreateServicePlan(ctx, db.CreateServicePlanParams{Slug: draft.Slug, Name: draft.Name, Description: draft.Description, Kind: db.PlanKind(draft.Kind), CreatedBy: actorID})
			if err != nil {
				return subscription.ServicePlan{}, translateSubscriptionError(err)
			}
			planID = created.ID
		} else {
			current, err := queries.GetServicePlanForUpdate(ctx, planID)
			if err != nil {
				return subscription.ServicePlan{}, translateSubscriptionError(err)
			}
			if current.Status == db.ServicePlanStatusArchived {
				return subscription.ServicePlan{}, subscription.ErrConflict
			}
		}
		versionNumber, err := queries.NextServicePlanVersion(ctx, planID)
		if err != nil {
			return subscription.ServicePlan{}, translateSubscriptionError(err)
		}
		version, err := queries.CreateServicePlanVersion(ctx, db.CreateServicePlanVersionParams{
			ServicePlanID: planID, Version: versionNumber, TokenQuota: draft.TokenQuota, ValidityDays: draft.ValidityDays,
			ConcurrencyLimit: draft.ConcurrencyLimit, RpmLimit: draft.RPMLimit, TpmLimit: draft.TPMLimit, CreatedBy: actorID,
		})
		if err != nil {
			return subscription.ServicePlan{}, translateSubscriptionError(err)
		}
		for _, route := range draft.Routes {
			if err := queries.CreateServicePlanVersionRoute(ctx, db.CreateServicePlanVersionRouteParams{ServicePlanVersionID: version.ID, ModelID: route.ModelID, ResourcePoolID: route.ResourcePoolID}); err != nil {
				return subscription.ServicePlan{}, translateSubscriptionError(err)
			}
		}
		if _, err := queries.PublishServicePlanVersion(ctx, db.PublishServicePlanVersionParams{Name: draft.Name, Description: draft.Description, Kind: db.PlanKind(draft.Kind), CurrentVersionID: &version.ID, ID: planID}); err != nil {
			return subscription.ServicePlan{}, translateSubscriptionError(err)
		}
		return servicePlanByID(ctx, queries, planID)
	})
}

func (r *SubscriptionRepository) SetPlanStatus(ctx context.Context, id uuid.UUID, status subscription.PlanStatus, actorID uuid.UUID, mutation subscription.Mutation) (subscription.ServicePlan, error) {
	return r.executePlanMutation(ctx, actorID, mutation, func(queries *db.Queries) (subscription.ServicePlan, error) {
		if _, err := queries.SetServicePlanStatus(ctx, db.SetServicePlanStatusParams{Status: db.ServicePlanStatus(status), ID: id}); err != nil {
			return subscription.ServicePlan{}, translateSubscriptionError(err)
		}
		return servicePlanByID(ctx, queries, id)
	})
}

func (r *SubscriptionRepository) executePlanMutation(ctx context.Context, actorID uuid.UUID, mutation subscription.Mutation, apply func(*db.Queries) (subscription.ServicePlan, error)) (subscription.ServicePlan, error) {
	tx, err := r.connections.Postgres.Begin(ctx)
	if err != nil {
		return subscription.ServicePlan{}, err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	operation, err := queries.ClaimServicePlanMutation(ctx, db.ClaimServicePlanMutationParams{ActorUserID: actorID, Action: mutation.Action, IdempotencyKey: mutation.IdempotencyKey, RequestFingerprint: mutation.RequestFingerprint, RequestID: mutation.RequestID})
	if errors.Is(err, pgx.ErrNoRows) {
		existing, loadErr := queries.GetServicePlanMutation(ctx, planMutationLookup(actorID, mutation))
		if loadErr != nil {
			return subscription.ServicePlan{}, translateSubscriptionError(loadErr)
		}
		return planMutationResult(existing, mutation)
	}
	if err != nil {
		return subscription.ServicePlan{}, translateSubscriptionError(err)
	}
	result, err := apply(queries)
	if err != nil {
		return subscription.ServicePlan{}, err
	}
	audit := auditParams(&actorID, mutation.Action, "service_plan", result.ID.String(), map[string]any{"status": result.Status, "current_version": result.CurrentVersion})
	audit.RequestID = &mutation.RequestID
	if _, err := queries.CreateAuditEvent(ctx, audit); err != nil {
		return subscription.ServicePlan{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return subscription.ServicePlan{}, err
	}
	if _, err := queries.CompleteServicePlanMutation(ctx, db.CompleteServicePlanMutationParams{ServicePlanID: &result.ID, Result: encoded, ID: operation.ID}); err != nil {
		return subscription.ServicePlan{}, err
	}
	if err := r.commitPlanMutation(ctx, tx); err != nil {
		return r.reconcilePlanMutation(ctx, actorID, mutation, err)
	}
	return result, nil
}

func planMutationLookup(actorID uuid.UUID, mutation subscription.Mutation) db.GetServicePlanMutationParams {
	return db.GetServicePlanMutationParams{ActorUserID: actorID, Action: mutation.Action, IdempotencyKey: mutation.IdempotencyKey}
}

func planMutationResult(operation db.ServicePlanMutation, mutation subscription.Mutation) (subscription.ServicePlan, error) {
	if !bytes.Equal(operation.RequestFingerprint, mutation.RequestFingerprint) {
		return subscription.ServicePlan{}, subscription.ErrIdempotencyConflict
	}
	var result subscription.ServicePlan
	if err := json.Unmarshal(operation.Result, &result); err != nil || operation.ServicePlanID == nil || *operation.ServicePlanID != result.ID {
		return subscription.ServicePlan{}, fmt.Errorf("subscription store: invalid service plan mutation result")
	}
	return result, nil
}

func (r *SubscriptionRepository) reconcilePlanMutation(ctx context.Context, actorID uuid.UUID, mutation subscription.Mutation, commitErr error) (subscription.ServicePlan, error) {
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	for delay := 20 * time.Millisecond; ; delay = minDuration(delay*2, 250*time.Millisecond) {
		operation, err := r.queries.GetServicePlanMutation(reconcileCtx, planMutationLookup(actorID, mutation))
		if err == nil {
			return planMutationResult(operation, mutation)
		}
		if !waitForReconcile(reconcileCtx, delay) {
			return subscription.ServicePlan{}, fmt.Errorf("%w: commit: %v; reconciliation: %v", subscription.ErrOutcomeUnknown, commitErr, err)
		}
	}
}

func (r *SubscriptionRepository) ListPlans(ctx context.Context, includeArchived bool) ([]subscription.ServicePlan, error) {
	rows, err := r.queries.ListServicePlans(ctx, includeArchived)
	if err != nil {
		return nil, translateSubscriptionError(err)
	}
	items := make([]subscription.ServicePlan, 0, len(rows))
	for _, row := range rows {
		plan, err := servicePlanFromParts(ctx, r.queries, row.ID, row.Slug, row.Name, row.Description, row.Kind, row.Status, row.CurrentVersionID, row.Version, row.TokenQuota, row.ValidityDays, row.ConcurrencyLimit, row.RpmLimit, row.TpmLimit, row.VersionCreatedAt.Time, row.CreatedAt.Time, row.UpdatedAt.Time)
		if err != nil {
			return nil, err
		}
		plan.ActiveSubscriptionCount = row.ActiveSubscriptionCount
		items = append(items, plan)
	}
	return items, nil
}

func (r *SubscriptionRepository) GetPlan(ctx context.Context, id uuid.UUID) (subscription.ServicePlan, error) {
	return servicePlanByID(ctx, r.queries, id)
}

func servicePlanByID(ctx context.Context, queries *db.Queries, id uuid.UUID) (subscription.ServicePlan, error) {
	row, err := queries.GetServicePlan(ctx, id)
	if err != nil {
		return subscription.ServicePlan{}, translateSubscriptionError(err)
	}
	return servicePlanFromParts(ctx, queries, row.ID, row.Slug, row.Name, row.Description, row.Kind, row.Status, row.CurrentVersionID, row.Version, row.TokenQuota, row.ValidityDays, row.ConcurrencyLimit, row.RpmLimit, row.TpmLimit, row.VersionCreatedAt.Time, row.CreatedAt.Time, row.UpdatedAt.Time)
}

func servicePlanFromParts(ctx context.Context, queries *db.Queries, id uuid.UUID, slug, name, description string, kind db.PlanKind, status db.ServicePlanStatus, versionID *uuid.UUID, version *int32, tokenQuota *int64, validityDays, concurrencyLimit *int32, rpmLimit *int32, tpmLimit *int64, versionCreatedAt, createdAt, updatedAt time.Time) (subscription.ServicePlan, error) {
	plan := subscription.ServicePlan{ID: id, Slug: slug, Name: name, Description: description, Kind: subscription.PlanKind(kind), Status: subscription.PlanStatus(status), CreatedAt: createdAt.UTC(), UpdatedAt: updatedAt.UTC()}
	if versionID == nil || version == nil || tokenQuota == nil || validityDays == nil || concurrencyLimit == nil {
		return plan, nil
	}
	routes, err := queries.ListServicePlanVersionRoutes(ctx, *versionID)
	if err != nil {
		return subscription.ServicePlan{}, translateSubscriptionError(err)
	}
	planRoutes := make([]subscription.PlanRoute, 0, len(routes))
	for _, route := range routes {
		planRoutes = append(planRoutes, subscription.PlanRoute{ModelID: route.ModelID, ModelName: route.ModelName, ResourcePoolID: route.ResourcePoolID, ResourcePoolName: route.ResourcePoolName, ResourcePoolSlug: route.ResourcePoolSlug, ProviderName: route.ProviderName})
	}
	plan.CurrentVersion = &subscription.PlanVersion{ID: *versionID, Version: *version, TokenQuota: *tokenQuota, ValidityDays: *validityDays, ConcurrencyLimit: *concurrencyLimit, RPMLimit: rpmLimit, TPMLimit: tpmLimit, Routes: planRoutes, CreatedAt: versionCreatedAt.UTC()}
	return plan, nil
}
