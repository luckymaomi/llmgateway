package store

import (
	"context"
	"errors"
	"math"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/luckymaomi/llmgateway/internal/costing"
	"github.com/luckymaomi/llmgateway/internal/execution"
	domainledger "github.com/luckymaomi/llmgateway/internal/ledger"
	"github.com/luckymaomi/llmgateway/internal/quota"
	db "github.com/luckymaomi/llmgateway/internal/store/db"
)

const quotaTransactionAttempts = 4

func (r *QuotaRepository) Settle(ctx context.Context, requestID uuid.UUID, claim execution.Claim, inputTokens, outputTokens int64, source quota.UsageSource) (quota.Resolution, error) {
	return r.resolve(ctx, resolutionCommand{requestID: requestID, claim: &claim, state: db.ReservationStateSettled, kind: db.LedgerEventKindSettlement, inputTokens: inputTokens, outputTokens: outputTokens, usageSource: source})
}

func (r *QuotaRepository) Release(ctx context.Context, requestID uuid.UUID, claim execution.Claim, errorKind, errorDetail string) (quota.Resolution, error) {
	return r.resolve(ctx, resolutionCommand{requestID: requestID, claim: &claim, state: db.ReservationStateReleased, kind: db.LedgerEventKindRelease, usageSource: quota.UsageUnknown, errorKind: errorKind, errorDetail: errorDetail})
}

func (r *QuotaRepository) ReleaseAccepted(ctx context.Context, requestID uuid.UUID, errorKind, errorDetail string) (quota.Resolution, error) {
	return r.resolve(ctx, resolutionCommand{requestID: requestID, state: db.ReservationStateReleased, kind: db.LedgerEventKindRelease, usageSource: quota.UsageUnknown, errorKind: errorKind, errorDetail: errorDetail})
}

func (r *QuotaRepository) Compensate(ctx context.Context, requestID uuid.UUID, claim execution.Claim, inputTokens, outputTokens int64, source quota.UsageSource, errorKind, errorDetail string) (quota.Resolution, error) {
	return r.resolve(ctx, resolutionCommand{requestID: requestID, claim: &claim, state: db.ReservationStateCompensated, kind: db.LedgerEventKindCompensation, inputTokens: inputTokens, outputTokens: outputTokens, usageSource: source, errorKind: errorKind, errorDetail: errorDetail})
}

type resolutionCommand struct {
	requestID                 uuid.UUID
	claim                     *execution.Claim
	state                     db.ReservationState
	kind                      db.LedgerEventKind
	inputTokens, outputTokens int64
	usageSource               quota.UsageSource
	errorKind, errorDetail    string
}

func (r *QuotaRepository) resolve(ctx context.Context, command resolutionCommand) (quota.Resolution, error) {
	var lastErr error
	for attempt := 0; attempt < quotaTransactionAttempts; attempt++ {
		resolved, err := r.resolveOnce(ctx, command)
		if err == nil {
			return resolved, nil
		}
		if !retryableTransaction(err) {
			return quota.Resolution{}, translateQuotaError(err)
		}
		lastErr = err
	}
	return quota.Resolution{}, translateQuotaError(lastErr)
}

func (r *QuotaRepository) resolveOnce(ctx context.Context, command resolutionCommand) (quota.Resolution, error) {
	tx, err := r.connections.Postgres.Begin(ctx)
	if err != nil {
		return quota.Resolution{}, err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	requestRecord, err := queries.GetRequestForUpdate(ctx, command.requestID)
	if err != nil {
		return quota.Resolution{}, err
	}
	reservationRecord, err := queries.GetLedgerReservationByRequestForUpdate(ctx, command.requestID)
	if errors.Is(err, pgx.ErrNoRows) {
		return quota.Resolution{}, quota.ErrInvariant
	}
	if err != nil {
		return quota.Resolution{}, err
	}
	if _, err := queries.GetSubscriptionForUpdate(ctx, reservationRecord.SubscriptionID); err != nil {
		return quota.Resolution{}, err
	}
	if err := validateResolutionFence(requestRecord, reservationRecord, command); err != nil {
		return quota.Resolution{}, err
	}

	chargeTokens, err := chargeFor(command)
	if err != nil {
		return quota.Resolution{}, err
	}
	inputCostNanos, outputCostNanos, totalCostNanos, err := costFor(requestRecord, command)
	if err != nil {
		return quota.Resolution{}, err
	}
	if reservationRecord.State != db.ReservationStateReserved {
		if !matchesTerminal(requestRecord, reservationRecord, command, chargeTokens, inputCostNanos, outputCostNanos, totalCostNanos) {
			return quota.Resolution{}, quota.ErrTerminalConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return quota.Resolution{}, err
		}
		return quota.Resolution{Request: requestFromDB(requestRecord), Reservation: reservationFromDB(reservationRecord)}, nil
	}

	requestID := requestRecord.ID
	reservationID := reservationRecord.ID
	eventRecord, err := queries.CreateLedgerEvent(ctx, db.CreateLedgerEventParams{
		UserID: requestRecord.UserID, SubscriptionID: reservationRecord.SubscriptionID, RequestID: &requestID, ReservationID: &reservationID,
		Kind: command.kind, TokenDelta: reservationRecord.ReservedTokens - chargeTokens, ReservedTokens: reservationRecord.ReservedTokens,
		InputTokens: command.inputTokens, OutputTokens: command.outputTokens, UsageSource: db.UsageSource(command.usageSource),
	})
	if err != nil {
		return quota.Resolution{}, err
	}
	reservationRecord, err = queries.CompleteLedgerReservation(ctx, db.CompleteLedgerReservationParams{
		State: command.state, ChargedTokens: chargeTokens, UsageSource: db.UsageSource(command.usageSource), TerminalEventID: &eventRecord.ID, ID: reservationRecord.ID,
	})
	if err != nil {
		return quota.Resolution{}, err
	}

	requestRecord, err = resolveRequestRecord(ctx, queries, requestRecord.ID, command, inputCostNanos, outputCostNanos, totalCostNanos)
	if err != nil {
		return quota.Resolution{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return quota.Resolution{}, err
	}
	return quota.Resolution{Request: requestFromDB(requestRecord), Reservation: reservationFromDB(reservationRecord)}, nil
}

func costFor(request db.Request, command resolutionCommand) (*int64, *int64, *int64, error) {
	if command.state == db.ReservationStateReleased {
		return nil, nil, nil, nil
	}
	inputCost, err := costing.Calculate(command.inputTokens, request.InputRateNanosPerMillion)
	if err != nil {
		return nil, nil, nil, quota.ErrInvalidInput
	}
	outputCost, err := costing.Calculate(command.outputTokens, request.OutputRateNanosPerMillion)
	if err != nil || inputCost > math.MaxInt64-outputCost {
		return nil, nil, nil, quota.ErrInvalidInput
	}
	totalCost := inputCost + outputCost
	return &inputCost, &outputCost, &totalCost, nil
}

func chargeFor(command resolutionCommand) (int64, error) {
	if command.state == db.ReservationStateReleased {
		return 0, nil
	}
	decision, err := domainledger.DecideUsage(domainledger.Usage{
		InputTokens: domainledger.Tokens(command.inputTokens), OutputTokens: domainledger.Tokens(command.outputTokens), Source: domainledger.UsageSource(command.usageSource),
	})
	if err != nil {
		return 0, quota.ErrInvalidInput
	}
	if decision.Disposition == domainledger.UsageHold {
		return 0, quota.ErrUsageUnknown
	}
	return int64(decision.ChargeTokens), nil
}

func resolveRequestRecord(ctx context.Context, queries *db.Queries, requestID uuid.UUID, command resolutionCommand, inputCostNanos, outputCostNanos, totalCostNanos *int64) (db.Request, error) {
	inputTokens, outputTokens := command.inputTokens, command.outputTokens
	var executionID *uuid.UUID
	var executionGeneration int64
	if command.claim != nil {
		executionID = &command.claim.ExecutionID
		executionGeneration = command.claim.Generation
	}
	switch command.state {
	case db.ReservationStateSettled:
		return queries.CompleteRequest(ctx, db.CompleteRequestParams{
			InputTokens: &inputTokens, OutputTokens: &outputTokens, UsageSource: db.UsageSource(command.usageSource),
			InputCostNanos: inputCostNanos, OutputCostNanos: outputCostNanos, TotalCostNanos: totalCostNanos,
			ID: requestID, ExecutionID: executionID, ExecutionGeneration: executionGeneration,
		})
	case db.ReservationStateReleased:
		errorKind := command.errorKind
		return queries.FailRequest(ctx, db.FailRequestParams{
			ErrorKind: &errorKind, ErrorDetail: optionalString(command.errorDetail), ID: requestID,
			ExecutionID: executionID, ExecutionGeneration: executionGeneration,
		})
	case db.ReservationStateCompensated:
		errorKind := command.errorKind
		return queries.FailRequestWithUsage(ctx, db.FailRequestWithUsageParams{
			InputTokens: &inputTokens, OutputTokens: &outputTokens, UsageSource: db.UsageSource(command.usageSource),
			InputCostNanos: inputCostNanos, OutputCostNanos: outputCostNanos, TotalCostNanos: totalCostNanos,
			ErrorKind: &errorKind, ErrorDetail: optionalString(command.errorDetail), ID: requestID,
			ExecutionID: executionID, ExecutionGeneration: executionGeneration,
		})
	default:
		return db.Request{}, quota.ErrInvariant
	}
}

func validateResolutionFence(request db.Request, reservation db.LedgerReservation, command resolutionCommand) error {
	if command.claim == nil {
		if request.ExecutionID != nil || request.ExecutionGeneration != 0 {
			return execution.ErrFenced
		}
		if reservation.State == db.ReservationStateReserved && request.Status != db.RequestStatusQueued {
			return execution.ErrFenced
		}
		return nil
	}
	claim := *command.claim
	if !claim.Valid() || claim.RequestID != request.ID || request.ExecutionID == nil || *request.ExecutionID != claim.ExecutionID || request.ExecutionGeneration != claim.Generation {
		return execution.ErrFenced
	}
	if reservation.State == db.ReservationStateReserved && request.Status != db.RequestStatusDispatching && request.Status != db.RequestStatusStreaming {
		return execution.ErrFenced
	}
	return nil
}

func matchesTerminal(request db.Request, reservation db.LedgerReservation, command resolutionCommand, chargeTokens int64, inputCostNanos, outputCostNanos, totalCostNanos *int64) bool {
	if reservation.State != command.state || reservation.ChargedTokens != chargeTokens || reservation.UsageSource != db.UsageSource(command.usageSource) {
		return false
	}
	if command.state == db.ReservationStateSettled {
		return request.Status == db.RequestStatusCompleted && equalInt64(request.InputTokens, command.inputTokens) && equalInt64(request.OutputTokens, command.outputTokens) &&
			equalInt64Pointers(request.InputCostNanos, inputCostNanos) && equalInt64Pointers(request.OutputCostNanos, outputCostNanos) && equalInt64Pointers(request.TotalCostNanos, totalCostNanos)
	}
	if request.Status != db.RequestStatusFailed || !equalString(request.ErrorKind, command.errorKind) || !equalOptionalString(request.ErrorDetail, command.errorDetail) {
		return false
	}
	return command.state == db.ReservationStateReleased || equalInt64(request.InputTokens, command.inputTokens) && equalInt64(request.OutputTokens, command.outputTokens) &&
		equalInt64Pointers(request.InputCostNanos, inputCostNanos) && equalInt64Pointers(request.OutputCostNanos, outputCostNanos) && equalInt64Pointers(request.TotalCostNanos, totalCostNanos)
}

func requestFromDB(value db.Request) quota.Request {
	return quota.Request{
		ID: value.ID, IdempotencyKey: value.IdempotencyKey, UserID: value.UserID, GatewayKeyID: value.GatewayKeyID,
		ModelID: value.ModelID, SubscriptionID: value.SubscriptionID, ResourcePoolID: value.ResourcePoolID,
		Status: quota.RequestStatus(value.Status), Stream: value.Stream,
		PriceVersionID: value.PriceVersionID, CostCurrency: value.CostCurrency,
		InputRateNanosPerMillion: value.InputRateNanosPerMillion, OutputRateNanosPerMillion: value.OutputRateNanosPerMillion,
		InputCostNanos: value.InputCostNanos, OutputCostNanos: value.OutputCostNanos, TotalCostNanos: value.TotalCostNanos,
		InputTokens: value.InputTokens, OutputTokens: value.OutputTokens, UsageSource: quota.UsageSource(value.UsageSource),
		ErrorKind: value.ErrorKind, ErrorDetail: value.ErrorDetail, AcceptedAt: value.AcceptedAt.Time.UTC(),
		CompletedAt: timePointer(value.CompletedAt), UpdatedAt: value.UpdatedAt.Time.UTC(),
	}
}

func reservationFromDB(value db.LedgerReservation) quota.Reservation {
	return quota.Reservation{
		ID: value.ID, SubscriptionID: value.SubscriptionID, RequestID: value.RequestID, State: quota.ReservationState(value.State),
		ReservedTokens: value.ReservedTokens, ChargedTokens: value.ChargedTokens, UsageSource: quota.UsageSource(value.UsageSource),
		ReserveEventID: value.ReserveEventID, TerminalEventID: value.TerminalEventID,
		CreatedAt: value.CreatedAt.Time.UTC(), UpdatedAt: value.UpdatedAt.Time.UTC(),
	}
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func equalInt64(value *int64, expected int64) bool {
	return value != nil && *value == expected
}

func equalInt64Pointers(value, expected *int64) bool {
	return value == nil && expected == nil || value != nil && expected != nil && *value == *expected
}

func equalString(value *string, expected string) bool {
	return value != nil && *value == expected
}

func equalOptionalString(value *string, expected string) bool {
	return value == nil && expected == "" || value != nil && *value == expected
}
