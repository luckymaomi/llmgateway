package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/luckymaomi/llmgateway/internal/responses"
	db "github.com/luckymaomi/llmgateway/internal/store/db"
)

type ResponseRepository struct {
	queries *db.Queries
}

func NewResponseRepository(connections *Connections) *ResponseRepository {
	return &ResponseRepository{queries: db.New(connections.Postgres)}
}

func (r *ResponseRepository) Create(ctx context.Context, record responses.EncryptedRecord) (responses.EncryptedRecord, error) {
	created, err := r.queries.CreateResponseRecord(ctx, db.CreateResponseRecordParams{
		ID: record.ID, RequestID: record.RequestID, GatewayKeyID: record.GatewayKeyID,
		PreviousResponseID: record.PreviousResponseID, Status: db.ResponseStatus(record.Status),
		Background: record.Background, EncryptedInput: record.EncryptedInput,
	})
	return responseRecordFromDB(created), translateResponseError(err)
}

func (r *ResponseRepository) CreateCompleted(ctx context.Context, record responses.EncryptedRecord) (responses.EncryptedRecord, error) {
	created, err := r.queries.CreateCompletedResponseRecord(ctx, db.CreateCompletedResponseRecordParams{
		ID: record.ID, RequestID: record.RequestID, GatewayKeyID: record.GatewayKeyID,
		PreviousResponseID: record.PreviousResponseID, EncryptedInput: record.EncryptedInput, EncryptedOutput: record.EncryptedOutput,
	})
	return responseRecordFromDB(created), translateResponseError(err)
}

func (r *ResponseRepository) CreateBackground(ctx context.Context, record responses.EncryptedRecord) (responses.EncryptedRecord, error) {
	created, err := r.queries.CreateBackgroundResponseRecord(ctx, db.CreateBackgroundResponseRecordParams{
		ID: record.ID, GatewayKeyID: record.GatewayKeyID, PreviousResponseID: record.PreviousResponseID,
		IdempotencyKey: record.IdempotencyKey, RequestDigest: record.RequestDigest,
		EncryptedInput: record.EncryptedInput, EncryptedRequest: record.EncryptedRequest,
	})
	return responseRecordFromDB(created), translateResponseError(err)
}

func (r *ResponseRepository) Complete(ctx context.Context, responseID uuid.UUID, encryptedOutput []byte) (responses.EncryptedRecord, error) {
	updated, err := r.queries.CompleteResponseRecord(ctx, db.CompleteResponseRecordParams{ID: responseID, EncryptedOutput: encryptedOutput})
	return responseRecordFromDB(updated), translateResponseError(err)
}

func (r *ResponseRepository) Fail(ctx context.Context, responseID uuid.UUID, encryptedError []byte) (responses.EncryptedRecord, error) {
	updated, err := r.queries.FailResponseRecord(ctx, db.FailResponseRecordParams{ID: responseID, EncryptedError: encryptedError})
	return responseRecordFromDB(updated), translateResponseError(err)
}

func (r *ResponseRepository) Get(ctx context.Context, responseID, gatewayKeyID uuid.UUID) (responses.EncryptedRecord, error) {
	record, err := r.queries.GetResponseRecord(ctx, db.GetResponseRecordParams{ID: responseID, GatewayKeyID: gatewayKeyID})
	return responseRecordFromDB(record), translateResponseError(err)
}

func (r *ResponseRepository) Delete(ctx context.Context, responseID, gatewayKeyID uuid.UUID) error {
	_, err := r.queries.DeleteResponseRecord(ctx, db.DeleteResponseRecordParams{ID: responseID, GatewayKeyID: gatewayKeyID})
	return translateResponseError(err)
}

func (r *ResponseRepository) RequestCancellation(ctx context.Context, responseID, gatewayKeyID uuid.UUID) (responses.EncryptedRecord, error) {
	record, err := r.queries.RequestResponseCancellation(ctx, db.RequestResponseCancellationParams{ID: responseID, GatewayKeyID: gatewayKeyID})
	return responseRecordFromDB(record), translateResponseError(err)
}

func (r *ResponseRepository) ClaimBackground(ctx context.Context, executionID uuid.UUID, staleBefore time.Time) (responses.EncryptedRecord, error) {
	record, err := r.queries.ClaimBackgroundResponse(ctx, db.ClaimBackgroundResponseParams{ExecutionID: &executionID, StaleBefore: timestamp(staleBefore)})
	return responseRecordFromDB(record), translateResponseError(err)
}

func (r *ResponseRepository) HeartbeatBackground(ctx context.Context, claim responses.Claim) error {
	_, err := r.queries.HeartbeatBackgroundResponse(ctx, db.HeartbeatBackgroundResponseParams{ID: claim.ResponseID, ExecutionID: &claim.ExecutionID, ExecutionGeneration: claim.Generation})
	return translateResponseExecutionError(err)
}

func (r *ResponseRepository) LinkBackgroundRequest(ctx context.Context, claim responses.Claim, requestID uuid.UUID) error {
	_, err := r.queries.LinkBackgroundResponseRequest(ctx, db.LinkBackgroundResponseRequestParams{
		ID: claim.ResponseID, ExecutionID: &claim.ExecutionID, ExecutionGeneration: claim.Generation, RequestID: &requestID,
	})
	return translateResponseExecutionError(err)
}

func (r *ResponseRepository) StageBackgroundOutput(ctx context.Context, claim responses.Claim, requestID uuid.UUID, encryptedOutput []byte) error {
	_, err := r.queries.StageBackgroundResponseOutput(ctx, db.StageBackgroundResponseOutputParams{
		ID: claim.ResponseID, ExecutionID: &claim.ExecutionID, ExecutionGeneration: claim.Generation,
		RequestID: &requestID, EncryptedOutput: encryptedOutput,
	})
	return translateResponseExecutionError(err)
}

func (r *ResponseRepository) CompleteBackground(ctx context.Context, claim responses.Claim, requestID uuid.UUID) error {
	_, err := r.queries.CompleteBackgroundResponse(ctx, db.CompleteBackgroundResponseParams{
		ID: claim.ResponseID, ExecutionID: &claim.ExecutionID, ExecutionGeneration: claim.Generation, RequestID: &requestID,
	})
	return translateResponseExecutionError(err)
}

func (r *ResponseRepository) TerminateBackground(ctx context.Context, claim responses.Claim, requestID *uuid.UUID, status responses.Status, encryptedError []byte) error {
	_, err := r.queries.TerminateBackgroundResponse(ctx, db.TerminateBackgroundResponseParams{
		ID: claim.ResponseID, ExecutionID: &claim.ExecutionID, ExecutionGeneration: claim.Generation,
		RequestID: requestID, Status: db.ResponseStatus(status), EncryptedError: encryptedError,
	})
	return translateResponseExecutionError(err)
}

func (r *ResponseRepository) ListBackgroundRecoveries(ctx context.Context, batchSize int32) ([]responses.Recovery, error) {
	rows, err := r.queries.ListBackgroundResponseRecoveries(ctx, batchSize)
	if err != nil {
		return nil, translateResponseError(err)
	}
	recoveries := make([]responses.Recovery, 0, len(rows))
	for _, row := range rows {
		recoveries = append(recoveries, responses.Recovery{
			ResponseID: row.ID, ResponseStatus: responses.Status(row.ResponseStatus), HasOutput: len(row.EncryptedOutput) > 0,
			RequestStatus: string(row.RequestStatus), ErrorKind: row.ErrorKind, ErrorDetail: row.ErrorDetail,
		})
	}
	return recoveries, nil
}

func (r *ResponseRepository) AttachBackgroundRequest(ctx context.Context, responseID uuid.UUID) error {
	_, err := r.queries.AttachBackgroundResponseRequest(ctx, responseID)
	return translateResponseError(err)
}

func (r *ResponseRepository) FinalizeRecoveredBackground(ctx context.Context, responseID uuid.UUID, status responses.Status, encryptedError []byte) error {
	_, err := r.queries.FinalizeRecoveredBackgroundResponse(ctx, db.FinalizeRecoveredBackgroundResponseParams{ID: responseID, Status: db.ResponseStatus(status), EncryptedError: encryptedError})
	return translateResponseError(err)
}

func responseRecordFromDB(record db.ResponseRecord) responses.EncryptedRecord {
	return responses.EncryptedRecord{
		ID: record.ID, RequestID: record.RequestID, GatewayKeyID: record.GatewayKeyID,
		PreviousResponseID: record.PreviousResponseID, IdempotencyKey: record.IdempotencyKey, RequestDigest: record.RequestDigest,
		Status: responses.Status(record.Status), Background: record.Background,
		EncryptedInput: record.EncryptedInput, EncryptedRequest: record.EncryptedRequest, EncryptedOutput: record.EncryptedOutput, EncryptedError: record.EncryptedError,
		ExecutionID: record.ExecutionID, ExecutionGeneration: record.ExecutionGeneration,
		ExecutionClaimedAt: timePointer(record.ExecutionClaimedAt), ExecutionHeartbeatAt: timePointer(record.ExecutionHeartbeatAt),
		CancelRequestedAt: timePointer(record.CancelRequestedAt), CompletedAt: timePointer(record.CompletedAt),
		CreatedAt: record.CreatedAt.Time.UTC(), UpdatedAt: record.UpdatedAt.Time.UTC(),
	}
}

func translateResponseExecutionError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return responses.ErrFenced
	}
	return translateResponseError(err)
}

func translateResponseError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return responses.ErrNotFound
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) {
		switch databaseError.Code {
		case "23503":
			return responses.ErrNotFound
		case "23505", "40001", "40P01":
			return responses.ErrConflict
		case "23514", "23502", "22P02":
			return responses.ErrInvalidInput
		}
	}
	return fmt.Errorf("response store: %w", err)
}
