package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/luckymaomi/llmgateway/internal/canonical"
	"github.com/luckymaomi/llmgateway/internal/execution"
	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/registry"
	"github.com/luckymaomi/llmgateway/internal/requestflow"
	"github.com/luckymaomi/llmgateway/internal/store/db"
)

type RequestRepository struct {
	connections *Connections
	queries     *db.Queries
}

func NewRequestRepository(connections *Connections) *RequestRepository {
	return &RequestRepository{connections: connections, queries: db.New(connections.Postgres)}
}

func (r *RequestRepository) ListAvailableModels(ctx context.Context, gatewayKeyID uuid.UUID) ([]requestflow.Model, error) {
	rows, err := r.queries.ListAvailableModelsForKey(ctx, gatewayKeyID)
	if err != nil {
		return nil, err
	}
	models := make([]requestflow.Model, 0, len(rows))
	for _, row := range rows {
		capabilities, err := decodeCapabilities(row.Capabilities)
		if err != nil {
			return nil, fmt.Errorf("decode capabilities for model %s: %w", row.ID, err)
		}
		models = append(models, requestflow.Model{
			ID: row.ID, PublicName: row.PublicName, UpstreamName: row.UpstreamName,
			ProviderID: row.ProviderID, ProviderSlug: row.ProviderSlug, ProviderKind: providers.Kind(row.ProviderKind), ProviderBaseURL: row.ProviderBaseUrl,
			Capabilities: capabilities, CreatedAt: row.CreatedAt.Time,
		})
	}
	return models, nil
}

func (r *RequestRepository) ResolveAvailableModel(ctx context.Context, gatewayKeyID uuid.UUID, publicName string) (requestflow.Model, error) {
	row, err := r.queries.ResolveAvailableModelForKey(ctx, db.ResolveAvailableModelForKeyParams{GatewayKeyID: gatewayKeyID, PublicName: publicName})
	if errors.Is(err, pgx.ErrNoRows) {
		return requestflow.Model{}, requestflow.ErrModelNotFound
	}
	if err != nil {
		return requestflow.Model{}, err
	}
	if !row.KeyAuthorized {
		return requestflow.Model{}, requestflow.ErrModelNotAuthorized
	}
	capabilities, err := decodeCapabilities(row.Capabilities)
	if err != nil {
		return requestflow.Model{}, fmt.Errorf("decode model capabilities: %w", err)
	}
	return requestflow.Model{
		ID: row.ID, PublicName: row.PublicName, UpstreamName: row.UpstreamName,
		ProviderID: row.ProviderID, ProviderSlug: row.ProviderSlug, ProviderKind: providers.Kind(row.ProviderKind), ProviderBaseURL: row.ProviderBaseUrl,
		Capabilities: capabilities, CreatedAt: row.CreatedAt.Time,
	}, nil
}

func (r *RequestRepository) ListResourcePoolCandidates(ctx context.Context, resourcePoolID, modelID uuid.UUID) ([]requestflow.Candidate, error) {
	rows, err := r.queries.ListResourcePoolCandidates(ctx, db.ListResourcePoolCandidatesParams{ResourcePoolID: resourcePoolID, ModelID: modelID})
	if err != nil {
		return nil, err
	}
	candidates := make([]requestflow.Candidate, 0, len(rows))
	for _, row := range rows {
		candidates = append(candidates, requestflow.Candidate{
			ID: row.ID, Priority: row.Priority, Weight: row.Weight, RPMLimit: row.RpmLimit, TPMLimit: row.TpmLimit,
			ConcurrencyLimit:    row.ConcurrencyLimit,
			ConsecutiveFailures: row.ConsecutiveFailures, LastSuccessAt: timePointer(row.LastSuccessAt), CooldownUntil: timePointer(row.CooldownUntil),
		})
	}
	return candidates, nil
}

func (r *RequestRepository) ClaimExecution(ctx context.Context, requestID, executionID uuid.UUID) (execution.Claim, error) {
	if requestID == uuid.Nil || executionID == uuid.Nil {
		return execution.Claim{}, execution.ErrNotClaimable
	}
	record, err := r.queries.ClaimRequestExecution(ctx, db.ClaimRequestExecutionParams{ID: requestID, ExecutionID: &executionID})
	if errors.Is(err, pgx.ErrNoRows) {
		return execution.Claim{}, execution.ErrNotClaimable
	}
	if err != nil {
		return execution.Claim{}, err
	}
	if record.ExecutionID == nil || *record.ExecutionID != executionID || record.ExecutionGeneration < 1 {
		return execution.Claim{}, execution.ErrFenced
	}
	return execution.Claim{RequestID: requestID, ExecutionID: executionID, Generation: record.ExecutionGeneration}, nil
}

func (r *RequestRepository) HeartbeatExecution(ctx context.Context, claim execution.Claim) error {
	if !claim.Valid() {
		return execution.ErrFenced
	}
	_, err := r.queries.HeartbeatRequestExecution(ctx, db.HeartbeatRequestExecutionParams{
		ID: claim.RequestID, ExecutionID: &claim.ExecutionID, ExecutionGeneration: claim.Generation,
	})
	return executionWriteError(err)
}

func (r *RequestRepository) MarkExecutionStreaming(ctx context.Context, claim execution.Claim, attemptID uuid.UUID, update requestflow.AttemptUpdate) error {
	if !claim.Valid() || attemptID == uuid.Nil || update.Status != "streaming" {
		return execution.ErrFenced
	}
	tx, err := r.connections.Postgres.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	attempt, err := queries.UpdateAttempt(ctx, attemptUpdateParams(claim, attemptID, update))
	if err != nil {
		return executionWriteError(err)
	}
	if err := recordCredentialObservation(ctx, queries, attempt.CredentialID, update.Credential); err != nil {
		return err
	}
	if _, err := queries.MarkRequestExecutionStreaming(ctx, db.MarkRequestExecutionStreamingParams{
		ID: claim.RequestID, ExecutionID: &claim.ExecutionID, ExecutionGeneration: claim.Generation,
	}); err != nil {
		return executionWriteError(err)
	}
	return tx.Commit(ctx)
}

func (r *RequestRepository) MarkExecutionUncertain(ctx context.Context, claim execution.Claim, attemptID uuid.UUID, update requestflow.AttemptUpdate, errorKind, errorDetail string) error {
	if !claim.Valid() || attemptID == uuid.Nil {
		return execution.ErrFenced
	}
	tx, err := r.connections.Postgres.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	if _, err := queries.UpdateAttempt(ctx, attemptUpdateParams(claim, attemptID, update)); err != nil {
		return executionWriteError(err)
	}
	if _, err := queries.MarkRequestExecutionUncertain(ctx, db.MarkRequestExecutionUncertainParams{
		ID: claim.RequestID, ExecutionID: &claim.ExecutionID, ExecutionGeneration: claim.Generation,
		ErrorKind: &errorKind, ErrorDetail: optionalString(errorDetail),
	}); err != nil {
		return executionWriteError(err)
	}
	return tx.Commit(ctx)
}

func (r *RequestRepository) RecoverStaleExecutions(ctx context.Context, staleBefore time.Time, batchSize int32) (int64, error) {
	if staleBefore.IsZero() || batchSize < 1 || batchSize > 1000 {
		return 0, fmt.Errorf("invalid stale execution recovery input")
	}
	return r.queries.RecoverStaleRequestExecutions(ctx, db.RecoverStaleRequestExecutionsParams{
		StaleBefore: optionalTimestamp(&staleBefore), BatchSize: batchSize,
	})
}

func (r *RequestRepository) ListRecoverableSettlements(ctx context.Context, staleBefore time.Time, batchSize int32) ([]requestflow.RecoverableSettlement, error) {
	if staleBefore.IsZero() || batchSize < 1 || batchSize > 1000 {
		return nil, fmt.Errorf("invalid recoverable settlement input")
	}
	rows, err := r.queries.ListRecoverableRequestSettlements(ctx, db.ListRecoverableRequestSettlementsParams{
		StaleBefore: optionalTimestamp(&staleBefore), BatchSize: batchSize,
	})
	if err != nil {
		return nil, err
	}
	settlements := make([]requestflow.RecoverableSettlement, 0, len(rows))
	for _, row := range rows {
		if row.ExecutionID == nil || row.InputTokens == nil || row.OutputTokens == nil || row.ExecutionGeneration < 1 {
			return nil, fmt.Errorf("recoverable settlement for request %s is incomplete", row.RequestID)
		}
		settlements = append(settlements, requestflow.RecoverableSettlement{
			Claim: execution.Claim{RequestID: row.RequestID, ExecutionID: *row.ExecutionID, Generation: row.ExecutionGeneration},
			Usage: requestflow.Usage{InputTokens: *row.InputTokens, OutputTokens: *row.OutputTokens, Source: canonical.UsageSource(row.UsageSource)},
		})
	}
	return settlements, nil
}

func (r *RequestRepository) ListStaleQueuedRequests(ctx context.Context, staleBefore time.Time, batchSize int32) ([]uuid.UUID, error) {
	if staleBefore.IsZero() || batchSize < 1 || batchSize > 1000 {
		return nil, fmt.Errorf("invalid stale queued request input")
	}
	return r.queries.ListStaleQueuedRequests(ctx, db.ListStaleQueuedRequestsParams{
		StaleBefore: optionalTimestamp(&staleBefore), BatchSize: batchSize,
	})
}

func (r *RequestRepository) CreateAttempt(ctx context.Context, claim execution.Claim, credentialID uuid.UUID, sequence int) (uuid.UUID, error) {
	if !claim.Valid() || credentialID == uuid.Nil || sequence < 1 {
		return uuid.Nil, execution.ErrFenced
	}
	attempt, err := r.queries.CreateAttempt(ctx, db.CreateAttemptParams{
		RequestID: claim.RequestID, ExecutionID: claim.ExecutionID, ExecutionGeneration: claim.Generation,
		CredentialID: credentialID, Sequence: int32(sequence), Status: db.AttemptStatusCreated,
	})
	if err != nil {
		return uuid.Nil, executionWriteError(err)
	}
	return attempt.ID, nil
}

func (r *RequestRepository) UpdateAttempt(ctx context.Context, claim execution.Claim, attemptID uuid.UUID, update requestflow.AttemptUpdate) error {
	if !claim.Valid() || attemptID == uuid.Nil {
		return execution.ErrFenced
	}
	if _, err := attemptStatus(update.Status); err != nil {
		return err
	}
	if update.Credential == nil {
		_, err := r.queries.UpdateAttempt(ctx, attemptUpdateParams(claim, attemptID, update))
		return executionWriteError(err)
	}
	tx, err := r.connections.Postgres.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	attempt, err := queries.UpdateAttempt(ctx, attemptUpdateParams(claim, attemptID, update))
	if err != nil {
		return executionWriteError(err)
	}
	if err := recordCredentialObservation(ctx, queries, attempt.CredentialID, update.Credential); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func recordCredentialObservation(ctx context.Context, queries *db.Queries, credentialID uuid.UUID, observation *requestflow.CredentialObservation) error {
	if observation == nil {
		return nil
	}
	if credentialID == uuid.Nil || observation.ObservedAt.IsZero() {
		return fmt.Errorf("invalid credential observation")
	}
	switch observation.Kind {
	case requestflow.CredentialSucceeded:
		return queries.RecordCredentialRuntimeSuccess(ctx, db.RecordCredentialRuntimeSuccessParams{
			ObservedAt: optionalTimestamp(&observation.ObservedAt), ID: credentialID,
		})
	case requestflow.CredentialFailed:
		if observation.ErrorKind == "" {
			return fmt.Errorf("credential failure requires an error kind")
		}
		errorKind := observation.ErrorKind
		return queries.RecordCredentialRuntimeFailure(ctx, db.RecordCredentialRuntimeFailureParams{
			CooldownUntil: optionalTimestamp(observation.CooldownUntil), ErrorKind: &errorKind, ID: credentialID,
		})
	default:
		return fmt.Errorf("invalid credential observation kind %q", observation.Kind)
	}
}

func attemptUpdateParams(claim execution.Claim, attemptID uuid.UUID, update requestflow.AttemptUpdate) db.UpdateAttemptParams {
	status, _ := attemptStatus(update.Status)
	var httpStatus *int32
	if update.HTTPStatus != nil {
		value := int32(*update.HTTPStatus)
		httpStatus = &value
	}
	params := db.UpdateAttemptParams{
		ID: attemptID, RequestID: claim.RequestID, ExecutionID: claim.ExecutionID, ExecutionGeneration: claim.Generation,
		Status: status, HttpStatus: httpStatus, UpstreamRequestID: update.UpstreamRequestID,
		ErrorKind: update.ErrorKind, RetryAfterAt: optionalTimestamp(update.RetryAfterAt), SentAt: optionalTimestamp(update.SentAt),
		FirstByteAt: optionalTimestamp(update.FirstByteAt), CompletedAt: optionalTimestamp(update.CompletedAt), UsageSource: db.UsageSourceUnknown,
	}
	if update.Usage != nil {
		params.InputTokens = &update.Usage.InputTokens
		params.OutputTokens = &update.Usage.OutputTokens
		params.UsageSource = db.UsageSource(update.Usage.Source)
	}
	return params
}

func executionWriteError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return execution.ErrFenced
	}
	return err
}

func decodeCapabilities(raw []byte) (registry.ModelCapabilities, error) {
	var capabilities registry.ModelCapabilities
	if err := json.Unmarshal(raw, &capabilities); err != nil {
		return registry.ModelCapabilities{}, err
	}
	return capabilities, nil
}

func attemptStatus(value string) (db.AttemptStatus, error) {
	switch db.AttemptStatus(value) {
	case db.AttemptStatusCreated, db.AttemptStatusSending, db.AttemptStatusStreaming,
		db.AttemptStatusCompleted, db.AttemptStatusFailed, db.AttemptStatusUncertain:
		return db.AttemptStatus(value), nil
	default:
		return "", fmt.Errorf("invalid attempt status %q", value)
	}
}
