package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
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

func (r *QuotaRepository) ListEntitlements(ctx context.Context, query quota.EntitlementQuery) (quota.PageResult[quota.Entitlement], error) {
	parameters := db.CountEntitlementsParams{UserID: query.UserID, Search: query.Search, Status: query.Status, ResourceDomain: string(query.ResourceDomain)}
	total, err := r.queries.CountEntitlements(ctx, parameters)
	if err != nil {
		return quota.PageResult[quota.Entitlement]{}, translateQuotaError(err)
	}
	rows, err := r.queries.ListEntitlementsWithBalance(ctx, db.ListEntitlementsWithBalanceParams{
		UserID: parameters.UserID, Search: parameters.Search, Status: parameters.Status, ResourceDomain: parameters.ResourceDomain,
		PageOffset: query.Page.Offset, PageSize: query.Page.Size,
	})
	if err != nil {
		return quota.PageResult[quota.Entitlement]{}, translateQuotaError(err)
	}
	items := make([]quota.Entitlement, 0, len(rows))
	for _, row := range rows {
		item := entitlementFromDB(db.Entitlement{
			ID: row.ID, UserID: row.UserID, Plan: row.Plan, ResourceDomain: row.ResourceDomain, ModelID: row.ModelID,
			GrantedTokens: row.GrantedTokens, StartsAt: row.StartsAt, ExpiresAt: row.ExpiresAt,
			ConcurrencyLimit: row.ConcurrencyLimit, RpmLimit: row.RpmLimit, TpmLimit: row.TpmLimit, CreatedAt: row.CreatedAt,
		}, row.BalanceTokens)
		item.OwnerName, item.ModelAlias = row.OwnerName, row.ModelAlias
		items = append(items, item)
	}
	return quota.PageResult[quota.Entitlement]{Items: items, Total: total}, nil
}

func (r *QuotaRepository) ListLedger(ctx context.Context, filter quota.LedgerFilter) (quota.PageResult[quota.LedgerEvent], error) {
	parameters := db.CountLedgerEventsParams{
		UserID: filter.UserID, EntitlementID: filter.EntitlementID, Search: filter.Search, ResourceDomain: string(filter.ResourceDomain),
	}
	total, err := r.queries.CountLedgerEvents(ctx, parameters)
	if err != nil {
		return quota.PageResult[quota.LedgerEvent]{}, translateQuotaError(err)
	}
	rows, err := r.queries.ListLedgerEvents(ctx, db.ListLedgerEventsParams{
		UserID: parameters.UserID, EntitlementID: parameters.EntitlementID, Search: parameters.Search, ResourceDomain: parameters.ResourceDomain,
		PageOffset: filter.Page.Offset, PageSize: filter.Page.Size,
	})
	if err != nil {
		return quota.PageResult[quota.LedgerEvent]{}, translateQuotaError(err)
	}
	items := make([]quota.LedgerEvent, 0, len(rows))
	for _, row := range rows {
		items = append(items, quota.LedgerEvent{
			ID: row.ID, UserID: row.UserID, EntitlementID: row.EntitlementID, RequestID: row.RequestID,
			ReservationID: row.ReservationID, Kind: quota.LedgerKind(row.Kind), TokenDelta: row.TokenDelta,
			ReservedTokens: row.ReservedTokens, InputTokens: row.InputTokens, OutputTokens: row.OutputTokens,
			UsageSource: quota.UsageSource(row.UsageSource), ResourceDomain: quota.ResourceDomain(row.ResourceDomain),
			Note: row.Note, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt.Time.UTC(),
			OwnerName: row.OwnerName, ActorName: row.ActorName,
		})
	}
	return quota.PageResult[quota.LedgerEvent]{Items: items, Total: total}, nil
}

func (r *QuotaRepository) ListRequestLogs(ctx context.Context, query quota.RequestLogQuery) (quota.PageResult[quota.RequestLog], error) {
	parameters := db.CountRequestLogsParams{
		UserID: query.UserID, GatewayKeyID: query.GatewayKeyID, ModelID: query.ModelID,
		Status: string(query.Status), FromTime: requiredTimestamp(query.From), ToTime: requiredTimestamp(query.To),
		Search: query.Search, ResourceDomain: string(query.ResourceDomain),
	}
	total, err := r.queries.CountRequestLogs(ctx, parameters)
	if err != nil {
		return quota.PageResult[quota.RequestLog]{}, translateQuotaError(err)
	}
	rows, err := r.queries.ListRequestLogs(ctx, db.ListRequestLogsParams{
		UserID: parameters.UserID, GatewayKeyID: parameters.GatewayKeyID, ModelID: parameters.ModelID,
		Status: parameters.Status, FromTime: parameters.FromTime, ToTime: parameters.ToTime,
		Search: parameters.Search, ResourceDomain: parameters.ResourceDomain,
		PageOffset: query.Page.Offset, PageSize: query.Page.Size,
	})
	if err != nil {
		return quota.PageResult[quota.RequestLog]{}, translateQuotaError(err)
	}
	items := make([]quota.RequestLog, 0, len(rows))
	for _, row := range rows {
		if !row.AcceptedAt.Valid || !row.UpdatedAt.Valid {
			return quota.PageResult[quota.RequestLog]{}, fmt.Errorf("quota store: request log row is incomplete")
		}
		items = append(items, quota.RequestLog{
			RequestID: row.ID, UserID: row.UserID, UserName: row.UserName,
			GatewayKeyID: row.GatewayKeyID, KeyPrefix: row.KeyPrefix, ModelID: row.ModelID, ModelAlias: row.ModelAlias,
			ResourceDomain: quota.ResourceDomain(row.ResourceDomain), Status: quota.RequestStatus(row.Status), Stream: row.Stream,
			InputTokens: row.InputTokens, OutputTokens: row.OutputTokens, UsageSource: quota.UsageSource(row.UsageSource),
			ErrorKind: row.ErrorKind, AcceptedAt: row.AcceptedAt.Time.UTC(), CompletedAt: timePointer(row.CompletedAt),
			UpdatedAt: row.UpdatedAt.Time.UTC(), AttemptCount: row.AttemptCount, LastAttemptStatus: optionalString(row.LastAttemptStatus),
		})
	}
	return quota.PageResult[quota.RequestLog]{Items: items, Total: total}, nil
}

func (r *QuotaRepository) GetRequestLog(ctx context.Context, requestID uuid.UUID, userID *uuid.UUID) (quota.RequestLogDetail, error) {
	row, err := r.queries.GetRequestLog(ctx, db.GetRequestLogParams{RequestID: requestID, UserID: userID})
	if err != nil {
		return quota.RequestLogDetail{}, translateQuotaError(err)
	}
	if !row.AcceptedAt.Valid || !row.UpdatedAt.Valid {
		return quota.RequestLogDetail{}, fmt.Errorf("quota store: request log row is incomplete")
	}
	result := quota.RequestLogDetail{RequestLog: quota.RequestLog{
		RequestID: row.ID, UserID: row.UserID, UserName: row.UserName,
		GatewayKeyID: row.GatewayKeyID, KeyPrefix: row.KeyPrefix, ModelID: row.ModelID, ModelAlias: row.ModelAlias,
		ResourceDomain: quota.ResourceDomain(row.ResourceDomain), Status: quota.RequestStatus(row.Status), Stream: row.Stream,
		InputTokens: row.InputTokens, OutputTokens: row.OutputTokens, UsageSource: quota.UsageSource(row.UsageSource),
		ErrorKind: row.ErrorKind, AcceptedAt: row.AcceptedAt.Time.UTC(), CompletedAt: timePointer(row.CompletedAt),
		UpdatedAt: row.UpdatedAt.Time.UTC(), AttemptCount: row.AttemptCount, LastAttemptStatus: optionalString(row.LastAttemptStatus),
	}}
	attempts, err := r.queries.ListRequestLogAttempts(ctx, requestID)
	if err != nil {
		return quota.RequestLogDetail{}, translateQuotaError(err)
	}
	result.Attempts = make([]quota.RequestAttempt, 0, len(attempts))
	for _, attempt := range attempts {
		if !attempt.CreatedAt.Valid {
			return quota.RequestLogDetail{}, fmt.Errorf("quota store: request attempt row is incomplete")
		}
		result.Attempts = append(result.Attempts, quota.RequestAttempt{
			ID: attempt.ID, Sequence: attempt.Sequence, Status: string(attempt.Status), ProviderName: attempt.ProviderName,
			CredentialName: attempt.CredentialName, UpstreamRequestID: attempt.UpstreamRequestID, HTTPStatus: attempt.HttpStatus,
			ErrorKind: attempt.ErrorKind, RetryAfterAt: timePointer(attempt.RetryAfterAt), SentAt: timePointer(attempt.SentAt),
			FirstByteAt: timePointer(attempt.FirstByteAt), CompletedAt: timePointer(attempt.CompletedAt),
			InputTokens: attempt.InputTokens, OutputTokens: attempt.OutputTokens, UsageSource: quota.UsageSource(attempt.UsageSource),
			CreatedAt: attempt.CreatedAt.Time.UTC(),
		})
	}
	return result, nil
}

func entitlementFromDB(value db.Entitlement, balance int64) quota.Entitlement {
	return quota.Entitlement{
		ID: value.ID, UserID: value.UserID, Plan: quota.Plan(value.Plan), ResourceDomain: quota.ResourceDomain(value.ResourceDomain),
		ModelID: value.ModelID, GrantedTokens: value.GrantedTokens, BalanceTokens: balance,
		StartsAt: value.StartsAt.Time.UTC(), ExpiresAt: value.ExpiresAt.Time.UTC(), ConcurrencyLimit: value.ConcurrencyLimit,
		RPMLimit: value.RpmLimit, TPMLimit: value.TpmLimit, CreatedAt: value.CreatedAt.Time.UTC(),
	}
}

func requiredTimestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
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
