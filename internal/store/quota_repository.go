package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/luckymaomi/llmgateway/internal/quota"
	db "github.com/luckymaomi/llmgateway/internal/store/db"
)

type QuotaRepository struct {
	connections               *Connections
	queries                   *db.Queries
	commitEntitlementMutation func(context.Context, pgx.Tx) error
}

func NewQuotaRepository(connections *Connections) *QuotaRepository {
	return &QuotaRepository{
		connections: connections,
		queries:     db.New(connections.Postgres),
		commitEntitlementMutation: func(ctx context.Context, tx pgx.Tx) error {
			return tx.Commit(ctx)
		},
	}
}

func (r *QuotaRepository) CreateEntitlement(ctx context.Context, input quota.NewEntitlement, actorID uuid.UUID) (quota.Entitlement, error) {
	tx, err := r.connections.Postgres.Begin(ctx)
	if err != nil {
		return quota.Entitlement{}, err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	lockKey := actorID.String() + ":" + input.IdempotencyKey.String()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
		return quota.Entitlement{}, err
	}
	idempotencyKey := input.IdempotencyKey
	existing, err := queries.GetEntitlementByGrantIdempotency(ctx, db.GetEntitlementByGrantIdempotencyParams{SourceEventID: &idempotencyKey, CreatedBy: &actorID})
	switch {
	case err == nil:
		if !grantMatches(existing, input) {
			return quota.Entitlement{}, quota.ErrConflict
		}
		balance, err := queries.EntitlementBalance(ctx, existing.ID)
		if err != nil {
			return quota.Entitlement{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return quota.Entitlement{}, err
		}
		return entitlementFromGrant(existing, balance), nil
	case !errors.Is(err, pgx.ErrNoRows):
		return quota.Entitlement{}, err
	}
	if input.ModelID != nil {
		modelDomain, err := queries.GetModelDomain(ctx, *input.ModelID)
		if err != nil {
			return quota.Entitlement{}, translateQuotaError(err)
		}
		if quota.ResourceDomain(modelDomain) != input.ResourceDomain {
			return quota.Entitlement{}, quota.ErrResourceDomainMismatch
		}
	}
	created, err := queries.CreateEntitlement(ctx, db.CreateEntitlementParams{
		UserID: input.UserID, Plan: db.PlanKind(input.Plan), ResourceDomain: db.ResourceDomain(input.ResourceDomain), ModelID: input.ModelID,
		GrantedTokens: input.GrantedTokens, StartsAt: timestamp(input.StartsAt), ExpiresAt: timestamp(input.ExpiresAt),
		ConcurrencyLimit: input.ConcurrencyLimit, RpmLimit: input.RPMLimit, TpmLimit: input.TPMLimit,
	})
	if err != nil {
		return quota.Entitlement{}, translateQuotaError(err)
	}
	note := input.Note
	if _, err := queries.CreateLedgerEvent(ctx, db.CreateLedgerEventParams{
		UserID: input.UserID, EntitlementID: created.ID, Kind: db.LedgerEventKindGrant, TokenDelta: input.GrantedTokens,
		UsageSource: db.UsageSourceUnknown, SourceEventID: &idempotencyKey, Note: &note, CreatedBy: &actorID,
	}); err != nil {
		return quota.Entitlement{}, translateQuotaError(err)
	}
	audit := auditParams(&actorID, "quota.entitlement_created", "entitlement", created.ID.String(), map[string]any{
		"user_id": input.UserID, "plan": input.Plan, "resource_domain": input.ResourceDomain, "model_id": input.ModelID, "granted_tokens": input.GrantedTokens,
	})
	audit.RequestID = &input.RequestID
	if _, err := queries.CreateAuditEvent(ctx, audit); err != nil {
		return quota.Entitlement{}, err
	}
	if err := r.commitEntitlementMutation(ctx, tx); err != nil {
		return r.reconcileEntitlementGrant(ctx, input, actorID, err)
	}
	return entitlementFromDB(created, input.GrantedTokens), nil
}

func (r *QuotaRepository) reconcileEntitlementGrant(ctx context.Context, input quota.NewEntitlement, actorID uuid.UUID, commitErr error) (quota.Entitlement, error) {
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	delay := 20 * time.Millisecond
	var reconciliationErr error
	for {
		existing, err := r.queries.GetEntitlementByGrantIdempotency(reconcileCtx, db.GetEntitlementByGrantIdempotencyParams{
			SourceEventID: &input.IdempotencyKey,
			CreatedBy:     &actorID,
		})
		if err == nil {
			if !grantMatches(existing, input) {
				return quota.Entitlement{}, quota.ErrConflict
			}
			balance, balanceErr := r.queries.EntitlementBalance(reconcileCtx, existing.ID)
			if balanceErr == nil {
				return entitlementFromGrant(existing, balance), nil
			}
			err = balanceErr
		}
		reconciliationErr = err
		timer := time.NewTimer(delay)
		select {
		case <-reconcileCtx.Done():
			timer.Stop()
			return quota.Entitlement{}, fmt.Errorf("%w: commit: %v; reconciliation: %v", quota.ErrOutcomeUnknown, commitErr, reconciliationErr)
		case <-timer.C:
		}
		if delay < 250*time.Millisecond {
			delay *= 2
		}
	}
}

func (r *QuotaRepository) ListEntitlements(ctx context.Context, userID *uuid.UUID, page quota.Page) ([]quota.Entitlement, error) {
	rows, err := r.queries.ListEntitlementsWithBalance(ctx, db.ListEntitlementsWithBalanceParams{UserID: userID, PageOffset: page.Offset, PageSize: page.Size})
	if err != nil {
		return nil, translateQuotaError(err)
	}
	items := make([]quota.Entitlement, 0, len(rows))
	for _, row := range rows {
		items = append(items, entitlementFromDB(db.Entitlement{
			ID: row.ID, UserID: row.UserID, Plan: row.Plan, ResourceDomain: row.ResourceDomain, ModelID: row.ModelID,
			GrantedTokens: row.GrantedTokens, StartsAt: row.StartsAt, ExpiresAt: row.ExpiresAt,
			ConcurrencyLimit: row.ConcurrencyLimit, RpmLimit: row.RpmLimit, TpmLimit: row.TpmLimit, CreatedAt: row.CreatedAt,
		}, row.BalanceTokens))
	}
	return items, nil
}

func (r *QuotaRepository) ListLedger(ctx context.Context, filter quota.LedgerFilter) ([]quota.LedgerEvent, error) {
	rows, err := r.queries.ListLedgerEvents(ctx, db.ListLedgerEventsParams{
		UserID: filter.UserID, EntitlementID: filter.EntitlementID, PageOffset: filter.Page.Offset, PageSize: filter.Page.Size,
	})
	if err != nil {
		return nil, translateQuotaError(err)
	}
	items := make([]quota.LedgerEvent, 0, len(rows))
	for _, row := range rows {
		items = append(items, ledgerEventFromDB(row))
	}
	return items, nil
}

func (r *QuotaRepository) ListUsage(ctx context.Context, userID *uuid.UUID, page quota.Page) ([]quota.UsageRecord, error) {
	rows, err := r.queries.ListRequestUsage(ctx, db.ListRequestUsageParams{UserID: userID, PageOffset: page.Offset, PageSize: page.Size})
	if err != nil {
		return nil, translateQuotaError(err)
	}
	items := make([]quota.UsageRecord, 0, len(rows))
	for _, row := range rows {
		if row.InputTokens == nil || row.OutputTokens == nil || !row.CompletedAt.Valid {
			return nil, fmt.Errorf("quota store: request usage row is incomplete")
		}
		items = append(items, quota.UsageRecord{
			RequestID: row.ID, UserID: row.UserID, KeyPrefix: row.KeyPrefix, ModelAlias: row.ModelAlias,
			ResourceDomain: quota.ResourceDomain(row.ResourceDomain), InputTokens: *row.InputTokens, OutputTokens: *row.OutputTokens,
			UsageSource: quota.UsageSource(row.UsageSource), OccurredAt: row.CompletedAt.Time.UTC(),
		})
	}
	return items, nil
}

func entitlementFromDB(value db.Entitlement, balance int64) quota.Entitlement {
	return quota.Entitlement{
		ID: value.ID, UserID: value.UserID, Plan: quota.Plan(value.Plan), ResourceDomain: quota.ResourceDomain(value.ResourceDomain),
		ModelID: value.ModelID, GrantedTokens: value.GrantedTokens, BalanceTokens: balance,
		StartsAt: value.StartsAt.Time.UTC(), ExpiresAt: value.ExpiresAt.Time.UTC(), ConcurrencyLimit: value.ConcurrencyLimit,
		RPMLimit: value.RpmLimit, TPMLimit: value.TpmLimit, CreatedAt: value.CreatedAt.Time.UTC(),
	}
}

func entitlementFromGrant(value db.GetEntitlementByGrantIdempotencyRow, balance int64) quota.Entitlement {
	return entitlementFromDB(db.Entitlement{
		ID: value.ID, UserID: value.UserID, Plan: value.Plan, ResourceDomain: value.ResourceDomain, ModelID: value.ModelID,
		GrantedTokens: value.GrantedTokens, StartsAt: value.StartsAt, ExpiresAt: value.ExpiresAt,
		ConcurrencyLimit: value.ConcurrencyLimit, RpmLimit: value.RpmLimit, TpmLimit: value.TpmLimit, CreatedAt: value.CreatedAt,
	}, balance)
}

func grantMatches(value db.GetEntitlementByGrantIdempotencyRow, input quota.NewEntitlement) bool {
	return value.UserID == input.UserID && value.Plan == db.PlanKind(input.Plan) && value.ResourceDomain == db.ResourceDomain(input.ResourceDomain) &&
		equalUUID(value.ModelID, input.ModelID) && value.GrantedTokens == input.GrantedTokens &&
		value.StartsAt.Time.UTC().Equal(input.StartsAt.UTC().Truncate(time.Microsecond)) && value.ExpiresAt.Time.UTC().Equal(input.ExpiresAt.UTC().Truncate(time.Microsecond)) &&
		value.ConcurrencyLimit == input.ConcurrencyLimit && equalInt32(value.RpmLimit, input.RPMLimit) && equalInt64Pointers(value.TpmLimit, input.TPMLimit) &&
		value.GrantNote != nil && *value.GrantNote == input.Note
}

func equalUUID(left, right *uuid.UUID) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func equalInt32(left, right *int32) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func equalInt64Pointers(left, right *int64) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func ledgerEventFromDB(value db.LedgerEvent) quota.LedgerEvent {
	return quota.LedgerEvent{
		ID: value.ID, UserID: value.UserID, EntitlementID: value.EntitlementID, RequestID: value.RequestID,
		ReservationID: value.ReservationID, Kind: quota.LedgerKind(value.Kind), TokenDelta: value.TokenDelta,
		ReservedTokens: value.ReservedTokens, InputTokens: value.InputTokens, OutputTokens: value.OutputTokens,
		UsageSource: quota.UsageSource(value.UsageSource), Note: value.Note, CreatedBy: value.CreatedBy, CreatedAt: value.CreatedAt.Time.UTC(),
	}
}

func translateQuotaError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return quota.ErrNotFound
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) {
		switch databaseError.Code {
		case "23503":
			return quota.ErrNotFound
		case "23505", "40001", "40P01":
			return quota.ErrConflict
		case "23514", "23502", "22P02":
			return quota.ErrInvalidInput
		}
	}
	return fmt.Errorf("quota store: %w", err)
}

func retryableTransaction(err error) bool {
	var databaseError *pgconn.PgError
	return errors.As(err, &databaseError) && (databaseError.Code == "40001" || databaseError.Code == "40P01")
}
