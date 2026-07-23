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
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/luckymaomi/llmgateway/internal/store/db"
	"github.com/luckymaomi/llmgateway/internal/subscription"
)

func (r *SubscriptionRepository) CreateSubscription(ctx context.Context, input subscription.NewSubscription, actorID uuid.UUID, mutation subscription.Mutation) (subscription.Subscription, error) {
	return r.executeSubscriptionMutation(ctx, actorID, mutation, func(queries *db.Queries) (subscription.Subscription, error) {
		user, err := queries.GetUserForGatewayKeyCreation(ctx, input.UserID)
		if err != nil {
			return subscription.Subscription{}, translateSubscriptionError(err)
		}
		if user.Status != db.UserStatusActive {
			return subscription.Subscription{}, subscription.ErrConflict
		}
		version, err := queries.GetCurrentServicePlanVersion(ctx, input.ServicePlanID)
		if err != nil {
			return subscription.Subscription{}, translateSubscriptionError(err)
		}
		status := db.SubscriptionStatusActive
		if input.StartsAt.After(time.Now().UTC()) {
			status = db.SubscriptionStatusScheduled
		}
		created, err := queries.CreateSubscription(ctx, db.CreateSubscriptionParams{
			UserID: input.UserID, ServicePlanVersionID: version.ID, Status: status, GrantedTokens: input.GrantedTokens,
			StartsAt: timestamp(input.StartsAt), ExpiresAt: timestamp(input.ExpiresAt), Notes: input.Notes, AssignedBy: actorID,
		})
		if err != nil {
			return subscription.Subscription{}, translateSubscriptionError(err)
		}
		if _, err := queries.CreateLedgerEvent(ctx, db.CreateLedgerEventParams{
			UserID: input.UserID, SubscriptionID: created.ID, Kind: db.LedgerEventKindGrant, TokenDelta: input.GrantedTokens,
			UsageSource: db.UsageSourceUnknown, SourceEventID: &mutation.IdempotencyKey, CreatedBy: &actorID,
		}); err != nil {
			return subscription.Subscription{}, translateSubscriptionError(err)
		}
		return subscriptionByID(ctx, queries, created.ID)
	})
}

func (r *SubscriptionRepository) UpdateSubscription(ctx context.Context, change subscription.SubscriptionChange, status subscription.SubscriptionStatus, actorID uuid.UUID, mutation subscription.Mutation) (subscription.Subscription, error) {
	return r.executeSubscriptionMutation(ctx, actorID, mutation, func(queries *db.Queries) (subscription.Subscription, error) {
		current, err := queries.GetSubscriptionForUpdate(ctx, change.ID)
		if err != nil {
			return subscription.Subscription{}, translateSubscriptionError(err)
		}
		if _, err := queries.UpdateSubscriptionTerm(ctx, db.UpdateSubscriptionTermParams{
			GrantedTokens: change.GrantedTokens, StartsAt: timestamp(change.StartsAt), ExpiresAt: timestamp(change.ExpiresAt),
			Notes: change.Notes, Status: db.SubscriptionStatus(status), ID: change.ID, ExpectedUpdatedAt: timestamp(change.ExpectedUpdatedAt),
		}); err != nil {
			return subscription.Subscription{}, translateSubscriptionError(err)
		}
		delta := change.GrantedTokens - current.GrantedTokens
		if delta != 0 {
			note := "subscription grant adjusted"
			if _, err := queries.CreateLedgerEvent(ctx, db.CreateLedgerEventParams{
				UserID: current.UserID, SubscriptionID: current.ID, Kind: db.LedgerEventKindAdjustment, TokenDelta: delta,
				UsageSource: db.UsageSourceUnknown, SourceEventID: &mutation.IdempotencyKey, Note: &note, CreatedBy: &actorID,
			}); err != nil {
				return subscription.Subscription{}, translateSubscriptionError(err)
			}
		}
		return subscriptionByID(ctx, queries, change.ID)
	})
}

func (r *SubscriptionRepository) SetSubscriptionStatus(ctx context.Context, id uuid.UUID, status subscription.SubscriptionStatus, expectedUpdatedAt time.Time, actorID uuid.UUID, mutation subscription.Mutation) (subscription.Subscription, error) {
	return r.executeSubscriptionMutation(ctx, actorID, mutation, func(queries *db.Queries) (subscription.Subscription, error) {
		if _, err := queries.UpdateSubscriptionStatus(ctx, db.UpdateSubscriptionStatusParams{Status: db.SubscriptionStatus(status), ID: id, ExpectedUpdatedAt: timestamp(expectedUpdatedAt)}); err != nil {
			return subscription.Subscription{}, translateSubscriptionError(err)
		}
		return subscriptionByID(ctx, queries, id)
	})
}

func (r *SubscriptionRepository) executeSubscriptionMutation(ctx context.Context, actorID uuid.UUID, mutation subscription.Mutation, apply func(*db.Queries) (subscription.Subscription, error)) (subscription.Subscription, error) {
	tx, err := r.connections.Postgres.Begin(ctx)
	if err != nil {
		return subscription.Subscription{}, err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	operation, err := queries.ClaimSubscriptionMutation(ctx, db.ClaimSubscriptionMutationParams{ActorUserID: actorID, Action: mutation.Action, IdempotencyKey: mutation.IdempotencyKey, RequestFingerprint: mutation.RequestFingerprint, RequestID: mutation.RequestID})
	if errors.Is(err, pgx.ErrNoRows) {
		existing, loadErr := queries.GetSubscriptionMutation(ctx, subscriptionMutationLookup(actorID, mutation))
		if loadErr != nil {
			return subscription.Subscription{}, translateSubscriptionError(loadErr)
		}
		return subscriptionMutationResult(existing, mutation)
	}
	if err != nil {
		return subscription.Subscription{}, translateSubscriptionError(err)
	}
	result, err := apply(queries)
	if err != nil {
		return subscription.Subscription{}, err
	}
	audit := auditParams(&actorID, mutation.Action, "subscription", result.ID.String(), map[string]any{"user_id": result.UserID, "plan_version_id": result.ServicePlanVersionID, "status": result.Status, "granted_tokens": result.GrantedTokens})
	audit.RequestID = &mutation.RequestID
	if _, err := queries.CreateAuditEvent(ctx, audit); err != nil {
		return subscription.Subscription{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return subscription.Subscription{}, err
	}
	if _, err := queries.CompleteSubscriptionMutation(ctx, db.CompleteSubscriptionMutationParams{SubscriptionID: &result.ID, Result: encoded, ID: operation.ID}); err != nil {
		return subscription.Subscription{}, err
	}
	if err := r.commitSubscriptionMutation(ctx, tx); err != nil {
		return r.reconcileSubscriptionMutation(ctx, actorID, mutation, err)
	}
	return result, nil
}

func subscriptionMutationLookup(actorID uuid.UUID, mutation subscription.Mutation) db.GetSubscriptionMutationParams {
	return db.GetSubscriptionMutationParams{ActorUserID: actorID, Action: mutation.Action, IdempotencyKey: mutation.IdempotencyKey}
}

func subscriptionMutationResult(operation db.SubscriptionMutation, mutation subscription.Mutation) (subscription.Subscription, error) {
	if !bytes.Equal(operation.RequestFingerprint, mutation.RequestFingerprint) {
		return subscription.Subscription{}, subscription.ErrIdempotencyConflict
	}
	var result subscription.Subscription
	if err := json.Unmarshal(operation.Result, &result); err != nil || operation.SubscriptionID == nil || *operation.SubscriptionID != result.ID {
		return subscription.Subscription{}, fmt.Errorf("subscription store: invalid subscription mutation result")
	}
	return result, nil
}

func (r *SubscriptionRepository) reconcileSubscriptionMutation(ctx context.Context, actorID uuid.UUID, mutation subscription.Mutation, commitErr error) (subscription.Subscription, error) {
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	for delay := 20 * time.Millisecond; ; delay = minDuration(delay*2, 250*time.Millisecond) {
		operation, err := r.queries.GetSubscriptionMutation(reconcileCtx, subscriptionMutationLookup(actorID, mutation))
		if err == nil {
			return subscriptionMutationResult(operation, mutation)
		}
		if !waitForReconcile(reconcileCtx, delay) {
			return subscription.Subscription{}, fmt.Errorf("%w: commit: %v; reconciliation: %v", subscription.ErrOutcomeUnknown, commitErr, err)
		}
	}
}

func (r *SubscriptionRepository) ListSubscriptions(ctx context.Context, query subscription.Query) (subscription.Page, error) {
	params := db.ListSubscriptionsParams{UserID: query.UserID, Search: query.Search, Status: query.Status, PageOffset: query.Offset, PageSize: query.Size}
	rows, err := r.queries.ListSubscriptions(ctx, params)
	if err != nil {
		return subscription.Page{}, translateSubscriptionError(err)
	}
	total, err := r.queries.CountSubscriptions(ctx, db.CountSubscriptionsParams{UserID: query.UserID, Search: query.Search, Status: query.Status})
	if err != nil {
		return subscription.Page{}, translateSubscriptionError(err)
	}
	items := make([]subscription.Subscription, 0, len(rows))
	routesByVersion := make(map[uuid.UUID][]subscription.PlanRoute)
	for _, row := range rows {
		routes, found := routesByVersion[row.ServicePlanVersionID]
		if !found {
			routes, err = subscriptionRoutes(ctx, r.queries, row.ServicePlanVersionID)
			if err != nil {
				return subscription.Page{}, err
			}
			routesByVersion[row.ServicePlanVersionID] = routes
		}
		item := subscriptionFromParts(row.ID, row.UserID, row.ServicePlanVersionID, row.ServicePlanID, row.MemberEmail, row.MemberName, row.ServicePlanName, row.PlanKind, row.PlanVersion, row.Status, row.GrantedTokens, row.BalanceTokens, row.StartsAt.Time, row.ExpiresAt.Time, row.Notes, row.ConcurrencyLimit, row.RpmLimit, row.TpmLimit, row.SuspendedAt, row.CanceledAt, row.CreatedAt.Time, row.UpdatedAt.Time)
		item.Routes = routes
		items = append(items, item)
	}
	return subscription.Page{Items: items, Total: total}, nil
}

func (r *SubscriptionRepository) GetSubscription(ctx context.Context, id uuid.UUID) (subscription.Subscription, error) {
	return subscriptionByID(ctx, r.queries, id)
}

func subscriptionByID(ctx context.Context, queries *db.Queries, id uuid.UUID) (subscription.Subscription, error) {
	row, err := queries.GetSubscription(ctx, id)
	if err != nil {
		return subscription.Subscription{}, translateSubscriptionError(err)
	}
	item := subscriptionFromParts(row.ID, row.UserID, row.ServicePlanVersionID, row.ServicePlanID, row.MemberEmail, row.MemberName, row.ServicePlanName, row.PlanKind, row.PlanVersion, row.Status, row.GrantedTokens, row.BalanceTokens, row.StartsAt.Time, row.ExpiresAt.Time, row.Notes, row.ConcurrencyLimit, row.RpmLimit, row.TpmLimit, row.SuspendedAt, row.CanceledAt, row.CreatedAt.Time, row.UpdatedAt.Time)
	item.Routes, err = subscriptionRoutes(ctx, queries, row.ServicePlanVersionID)
	if err != nil {
		return subscription.Subscription{}, err
	}
	return item, nil
}

func subscriptionRoutes(ctx context.Context, queries *db.Queries, versionID uuid.UUID) ([]subscription.PlanRoute, error) {
	rows, err := queries.ListServicePlanVersionRoutes(ctx, versionID)
	if err != nil {
		return nil, translateSubscriptionError(err)
	}
	routes := make([]subscription.PlanRoute, 0, len(rows))
	for _, row := range rows {
		routes = append(routes, subscription.PlanRoute{
			ModelID: row.ModelID, ModelName: row.ModelName, ResourcePoolID: row.ResourcePoolID,
			ResourcePoolName: row.ResourcePoolName, ResourcePoolSlug: row.ResourcePoolSlug, ProviderName: row.ProviderName,
		})
	}
	return routes, nil
}

func subscriptionFromParts(id, userID, versionID, planID uuid.UUID, email, memberName, planName string, kind db.PlanKind, version int32, persistedStatus db.SubscriptionStatus, granted, balance int64, startsAt, expiresAt time.Time, notes string, concurrency int32, rpm *int32, tpm *int64, suspendedAt, canceledAt pgtype.Timestamptz, createdAt, updatedAt time.Time) subscription.Subscription {
	status := subscription.SubscriptionStatus(persistedStatus)
	now := time.Now().UTC()
	if status != subscription.StatusSuspended && status != subscription.StatusCanceled {
		if !expiresAt.After(now) {
			status = subscription.StatusExpired
		} else if startsAt.After(now) {
			status = subscription.StatusScheduled
		} else {
			status = subscription.StatusActive
		}
	}
	return subscription.Subscription{
		ID: id, UserID: userID, MemberEmail: email, MemberName: memberName, ServicePlanID: planID, ServicePlanVersionID: versionID,
		ServicePlanName: planName, PlanKind: subscription.PlanKind(kind), PlanVersion: version, Status: status,
		GrantedTokens: granted, BalanceTokens: balance, StartsAt: startsAt.UTC(), ExpiresAt: expiresAt.UTC(), Notes: notes,
		ConcurrencyLimit: concurrency, RPMLimit: rpm, TPMLimit: tpm, SuspendedAt: timePointer(suspendedAt), CanceledAt: timePointer(canceledAt), CreatedAt: createdAt.UTC(), UpdatedAt: updatedAt.UTC(),
	}
}

func translateSubscriptionError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return subscription.ErrNotFound
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) {
		switch databaseError.Code {
		case "23503":
			return subscription.ErrNotFound
		case "23505", "40001", "55000":
			return subscription.ErrConflict
		}
	}
	return fmt.Errorf("subscription store: %w", err)
}
