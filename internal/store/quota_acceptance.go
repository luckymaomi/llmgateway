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
	if input.RequestID == uuid.Nil {
		return quota.AcceptedRequest{}, quota.ErrInvalidInput
	}
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

	_, err = queries.GetActiveGatewayKeyForRequest(ctx, db.GetActiveGatewayKeyForRequestParams{GatewayKeyID: input.GatewayKeyID, UserID: input.UserID})
	if errors.Is(err, pgx.ErrNoRows) {
		return quota.AcceptedRequest{}, quota.ErrForbidden
	}
	if err != nil {
		return quota.AcceptedRequest{}, err
	}
	modelDomain, err := queries.GetAuthorizedGatewayKeyModelDomain(ctx, db.GetAuthorizedGatewayKeyModelDomainParams{
		GatewayKeyID: input.GatewayKeyID, ModelID: input.ModelID, RevisionID: *input.ConfigRevisionID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return quota.AcceptedRequest{}, quota.ErrModelNotAuthorized
	}
	if err != nil {
		return quota.AcceptedRequest{}, err
	}
	if quota.ResourceDomain(modelDomain) != input.ResourceDomain {
		return quota.AcceptedRequest{}, quota.ErrResourceDomainMismatch
	}
	price, err := queries.GetEffectiveModelPrice(ctx, input.ModelID)
	if errors.Is(err, pgx.ErrNoRows) {
		return quota.AcceptedRequest{}, quota.ErrCostConfigurationMissing
	}
	if err != nil {
		return quota.AcceptedRequest{}, err
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

	requestID := input.RequestID
	requestRecord, err := queries.CreateRequest(ctx, db.CreateRequestParams{
		ID: requestID, IdempotencyKey: input.IdempotencyKey, RequestDigest: input.RequestDigest, UserID: input.UserID,
		GatewayKeyID: input.GatewayKeyID, ModelID: input.ModelID, EntitlementID: entitlement.ID,
		ConfigRevisionID: input.ConfigRevisionID, ResourceDomain: db.ResourceDomain(input.ResourceDomain), Status: db.RequestStatusQueued, Stream: input.Stream,
		PriceVersionID: price.ID, CostCurrency: price.Currency,
		InputRateNanosPerMillion: price.InputRateNanosPerMillion, OutputRateNanosPerMillion: price.OutputRateNanosPerMillion,
	})
	if err != nil {
		return quota.AcceptedRequest{}, err
	}
	reservationID := uuid.New()
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
	return quota.AcceptedRequest{
		Request: requestFromDB(requestRecord), Reservation: reservationFromDB(reservationRecord),
		EntitlementCapacity: entitlementCapacityFromDB(*entitlement),
	}, nil
}

func (r *QuotaRepository) replayAcceptedRequest(ctx context.Context, tx pgx.Tx, queries *db.Queries, input quota.AcceptInput, existing db.Request) (quota.AcceptedRequest, error) {
	if existing.UserID != input.UserID || existing.GatewayKeyID != input.GatewayKeyID || existing.ModelID != input.ModelID || !equalUUID(existing.ConfigRevisionID, input.ConfigRevisionID) || existing.ResourceDomain != db.ResourceDomain(input.ResourceDomain) || existing.Stream != input.Stream || !bytes.Equal(existing.RequestDigest, input.RequestDigest) {
		return quota.AcceptedRequest{}, quota.ErrConflict
	}
	reservationRecord, err := queries.GetLedgerReservationByRequest(ctx, existing.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return quota.AcceptedRequest{}, quota.ErrInvariant
	}
	if err != nil {
		return quota.AcceptedRequest{}, err
	}
	entitlement, err := queries.GetEntitlementForUpdate(ctx, existing.EntitlementID)
	if err != nil {
		return quota.AcceptedRequest{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return quota.AcceptedRequest{}, err
	}
	return quota.AcceptedRequest{
		Request: requestFromDB(existing), Reservation: reservationFromDB(reservationRecord),
		EntitlementCapacity: entitlementCapacityFromDB(entitlement), Replayed: true,
	}, nil
}

func entitlementCapacityFromDB(value db.Entitlement) quota.EntitlementCapacity {
	return quota.EntitlementCapacity{
		ID: value.ID, ConcurrencyLimit: value.ConcurrencyLimit,
		RPMLimit: value.RpmLimit, TPMLimit: value.TpmLimit,
	}
}
