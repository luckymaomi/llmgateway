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
	if _, err := queries.GetActiveGatewayKeyForRequest(ctx, db.GetActiveGatewayKeyForRequestParams{GatewayKeyID: input.GatewayKeyID, UserID: input.UserID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return quota.AcceptedRequest{}, quota.ErrForbidden
		}
		return quota.AcceptedRequest{}, err
	}
	bindings, err := queries.ListGatewayKeyModelBindingsByKey(ctx, input.GatewayKeyID)
	if err != nil {
		return quota.AcceptedRequest{}, err
	}
	authorized := false
	for _, binding := range bindings {
		if binding.ModelID == input.ModelID {
			authorized = true
			break
		}
	}
	if !authorized {
		return quota.AcceptedRequest{}, quota.ErrModelNotAuthorized
	}
	price, err := queries.GetEffectiveModelPrice(ctx, input.ModelID)
	if errors.Is(err, pgx.ErrNoRows) {
		return quota.AcceptedRequest{}, quota.ErrCostConfigurationMissing
	}
	if err != nil {
		return quota.AcceptedRequest{}, err
	}
	routes, err := queries.GetApplicableSubscriptionRoutesForUpdate(ctx, db.GetApplicableSubscriptionRoutesForUpdateParams{UserID: input.UserID, ModelID: input.ModelID})
	if err != nil {
		return quota.AcceptedRequest{}, err
	}
	var selected *db.GetApplicableSubscriptionRoutesForUpdateRow
	for index := range routes {
		balance, err := queries.SubscriptionBalance(ctx, routes[index].ID)
		if err != nil {
			return quota.AcceptedRequest{}, err
		}
		if balance >= input.ReservedTokens {
			selected = &routes[index]
			break
		}
	}
	if selected == nil {
		return quota.AcceptedRequest{}, quota.ErrQuotaExhausted
	}
	requestRecord, err := queries.CreateRequest(ctx, db.CreateRequestParams{
		ID: input.RequestID, IdempotencyKey: input.IdempotencyKey, RequestDigest: input.RequestDigest,
		UserID: input.UserID, GatewayKeyID: input.GatewayKeyID, ModelID: input.ModelID,
		SubscriptionID: selected.ID, ResourcePoolID: selected.ResourcePoolID,
		PriceVersionID: price.ID, CostCurrency: price.Currency, InputRateNanosPerMillion: price.InputRateNanosPerMillion,
		OutputRateNanosPerMillion: price.OutputRateNanosPerMillion, Status: db.RequestStatusQueued, Stream: input.Stream,
	})
	if err != nil {
		return quota.AcceptedRequest{}, err
	}
	reservationID := uuid.New()
	reservationEvent, err := queries.CreateLedgerEvent(ctx, db.CreateLedgerEventParams{
		UserID: input.UserID, SubscriptionID: selected.ID, RequestID: &requestRecord.ID, ReservationID: &reservationID,
		Kind: db.LedgerEventKindReservation, TokenDelta: -input.ReservedTokens, ReservedTokens: input.ReservedTokens,
		UsageSource: db.UsageSourceEstimated, SourceEventID: &reservationID,
	})
	if err != nil {
		return quota.AcceptedRequest{}, err
	}
	reservation, err := queries.CreateLedgerReservation(ctx, db.CreateLedgerReservationParams{ID: reservationID, SubscriptionID: selected.ID, RequestID: requestRecord.ID, ReservedTokens: input.ReservedTokens, ReserveEventID: reservationEvent.ID})
	if err != nil {
		return quota.AcceptedRequest{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return quota.AcceptedRequest{}, err
	}
	return quota.AcceptedRequest{Request: requestFromDB(requestRecord), Reservation: reservationFromDB(reservation), SubscriptionCapacity: subscriptionCapacity(selected.ID, selected.ConcurrencyLimit, selected.RpmLimit, selected.TpmLimit)}, nil
}

func (r *QuotaRepository) replayAcceptedRequest(ctx context.Context, tx pgx.Tx, queries *db.Queries, input quota.AcceptInput, existing db.Request) (quota.AcceptedRequest, error) {
	if existing.UserID != input.UserID || existing.GatewayKeyID != input.GatewayKeyID || existing.ModelID != input.ModelID || existing.Stream != input.Stream || !bytes.Equal(existing.RequestDigest, input.RequestDigest) {
		return quota.AcceptedRequest{}, quota.ErrConflict
	}
	reservation, err := queries.GetLedgerReservationByRequest(ctx, existing.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return quota.AcceptedRequest{}, quota.ErrInvariant
	}
	if err != nil {
		return quota.AcceptedRequest{}, err
	}
	subscriptionRecord, err := queries.GetSubscription(ctx, existing.SubscriptionID)
	if err != nil {
		return quota.AcceptedRequest{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return quota.AcceptedRequest{}, err
	}
	return quota.AcceptedRequest{
		Request: requestFromDB(existing), Reservation: reservationFromDB(reservation), Replayed: true,
		SubscriptionCapacity: subscriptionCapacity(subscriptionRecord.ID, subscriptionRecord.ConcurrencyLimit, subscriptionRecord.RpmLimit, subscriptionRecord.TpmLimit),
	}, nil
}

func subscriptionCapacity(id uuid.UUID, concurrency int32, rpm *int32, tpm *int64) quota.SubscriptionCapacity {
	return quota.SubscriptionCapacity{ID: id, ConcurrencyLimit: concurrency, RPMLimit: rpm, TPMLimit: tpm}
}
