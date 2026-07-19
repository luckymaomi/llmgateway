package store

import (
	"bytes"
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/luckymaomi/llmgateway/internal/quota"
	db "github.com/luckymaomi/llmgateway/internal/store/db"
)

func (r *QuotaRepository) AcceptRequest(ctx context.Context, input quota.AcceptInput) (quota.AcceptedRequest, error) {
	var lastErr error
	for attempt := 0; attempt < quotaTransactionAttempts; attempt++ {
		accepted, err := r.acceptRequestOnce(ctx, input)
		if err == nil {
			return accepted, nil
		}
		if !retryableTransaction(err) {
			return quota.AcceptedRequest{}, translateQuotaError(err)
		}
		lastErr = err
	}
	return quota.AcceptedRequest{}, translateQuotaError(lastErr)
}

func (r *QuotaRepository) acceptRequestOnce(ctx context.Context, input quota.AcceptInput) (quota.AcceptedRequest, error) {
	tx, err := r.connections.Postgres.Begin(ctx)
	if err != nil {
		return quota.AcceptedRequest{}, err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)

	if input.IdempotencyKey != nil {
		lockKey := input.GatewayKeyID.String() + ":" + *input.IdempotencyKey
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", lockKey); err != nil {
			return quota.AcceptedRequest{}, err
		}
		existing, err := queries.GetRequestByIdempotencyKey(ctx, db.GetRequestByIdempotencyKeyParams{GatewayKeyID: input.GatewayKeyID, IdempotencyKey: input.IdempotencyKey})
		switch {
		case err == nil:
			return r.replayAcceptedRequest(ctx, tx, queries, input, existing)
		case !errors.Is(err, pgx.ErrNoRows):
			return quota.AcceptedRequest{}, err
		}
	}

	keyOwned, err := queries.IsGatewayKeyOwnedByUser(ctx, db.IsGatewayKeyOwnedByUserParams{GatewayKeyID: input.GatewayKeyID, UserID: input.UserID})
	if err != nil {
		return quota.AcceptedRequest{}, err
	}
	if !keyOwned {
		return quota.AcceptedRequest{}, quota.ErrForbidden
	}
	modelDomain, err := queries.GetAuthorizedModelDomain(ctx, db.GetAuthorizedModelDomainParams{UserID: input.UserID, ModelID: input.ModelID})
	if errors.Is(err, pgx.ErrNoRows) {
		return quota.AcceptedRequest{}, quota.ErrModelNotAuthorized
	}
	if err != nil {
		return quota.AcceptedRequest{}, err
	}
	if quota.ResourceDomain(modelDomain) != input.ResourceDomain {
		return quota.AcceptedRequest{}, quota.ErrResourceDomainMismatch
	}

	modelID := input.ModelID
	applicable, err := queries.ListApplicableEntitlementsForUpdate(ctx, db.ListApplicableEntitlementsForUpdateParams{
		UserID: input.UserID, ResourceDomain: db.ResourceDomain(input.ResourceDomain), ModelID: &modelID,
	})
	if err != nil {
		return quota.AcceptedRequest{}, err
	}
	var entitlement *db.Entitlement
	for index := range applicable {
		balance, err := queries.EntitlementBalance(ctx, applicable[index].ID)
		if err != nil {
			return quota.AcceptedRequest{}, err
		}
		if balance >= input.ReservedTokens {
			entitlement = &applicable[index]
			break
		}
	}
	if entitlement == nil {
		return quota.AcceptedRequest{}, quota.ErrQuotaExhausted
	}

	requestRecord, err := queries.CreateRequest(ctx, db.CreateRequestParams{
		IdempotencyKey: input.IdempotencyKey, RequestDigest: input.RequestDigest, UserID: input.UserID,
		GatewayKeyID: input.GatewayKeyID, ModelID: input.ModelID, EntitlementID: entitlement.ID,
		ConfigRevisionID: input.ConfigRevisionID, ResourceDomain: db.ResourceDomain(input.ResourceDomain), Status: db.RequestStatusQueued, Stream: input.Stream,
	})
	if err != nil {
		return quota.AcceptedRequest{}, err
	}
	reservationID := uuid.New()
	requestID := requestRecord.ID
	reservationEvent, err := queries.CreateLedgerEvent(ctx, db.CreateLedgerEventParams{
		UserID: input.UserID, EntitlementID: entitlement.ID, RequestID: &requestID, ReservationID: &reservationID,
		Kind: db.LedgerEventKindReservation, TokenDelta: -input.ReservedTokens, ReservedTokens: input.ReservedTokens,
		UsageSource: db.UsageSourceEstimated, SourceEventID: &reservationID,
	})
	if err != nil {
		return quota.AcceptedRequest{}, err
	}
	reservationRecord, err := queries.CreateLedgerReservation(ctx, db.CreateLedgerReservationParams{
		ID: reservationID, EntitlementID: entitlement.ID, RequestID: requestRecord.ID,
		ReservedTokens: input.ReservedTokens, ReserveEventID: reservationEvent.ID,
	})
	if err != nil {
		return quota.AcceptedRequest{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return quota.AcceptedRequest{}, err
	}
	return quota.AcceptedRequest{Request: requestFromDB(requestRecord), Reservation: reservationFromDB(reservationRecord)}, nil
}

func (r *QuotaRepository) replayAcceptedRequest(ctx context.Context, tx pgx.Tx, queries *db.Queries, input quota.AcceptInput, existing db.Request) (quota.AcceptedRequest, error) {
	if existing.UserID != input.UserID || existing.GatewayKeyID != input.GatewayKeyID || existing.ModelID != input.ModelID || existing.ResourceDomain != db.ResourceDomain(input.ResourceDomain) || existing.Stream != input.Stream || !bytes.Equal(existing.RequestDigest, input.RequestDigest) {
		return quota.AcceptedRequest{}, quota.ErrConflict
	}
	reservationRecord, err := queries.GetLedgerReservationByRequest(ctx, existing.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return quota.AcceptedRequest{}, quota.ErrInvariant
	}
	if err != nil {
		return quota.AcceptedRequest{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return quota.AcceptedRequest{}, err
	}
	return quota.AcceptedRequest{Request: requestFromDB(existing), Reservation: reservationFromDB(reservationRecord), Replayed: true}, nil
}
