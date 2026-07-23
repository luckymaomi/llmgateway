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
	connections *Connections
	queries     *db.Queries
}

func NewQuotaRepository(connections *Connections) *QuotaRepository {
	return &QuotaRepository{connections: connections, queries: db.New(connections.Postgres)}
}

func (r *QuotaRepository) ListLedger(ctx context.Context, filter quota.LedgerFilter) (quota.PageResult[quota.LedgerEvent], error) {
	params := db.CountLedgerEventsParams{UserID: filter.UserID, SubscriptionID: filter.SubscriptionID, Search: filter.Search}
	total, err := r.queries.CountLedgerEvents(ctx, params)
	if err != nil {
		return quota.PageResult[quota.LedgerEvent]{}, translateQuotaError(err)
	}
	rows, err := r.queries.ListLedgerEvents(ctx, db.ListLedgerEventsParams{UserID: params.UserID, SubscriptionID: params.SubscriptionID, Search: params.Search, PageOffset: filter.Page.Offset, PageSize: filter.Page.Size})
	if err != nil {
		return quota.PageResult[quota.LedgerEvent]{}, translateQuotaError(err)
	}
	items := make([]quota.LedgerEvent, 0, len(rows))
	for _, row := range rows {
		items = append(items, quota.LedgerEvent{
			ID: row.ID, UserID: row.UserID, SubscriptionID: row.SubscriptionID, ServicePlanName: row.ServicePlanName,
			RequestID: row.RequestID, ReservationID: row.ReservationID, Kind: quota.LedgerKind(row.Kind), TokenDelta: row.TokenDelta,
			ReservedTokens: row.ReservedTokens, InputTokens: row.InputTokens, OutputTokens: row.OutputTokens,
			UsageSource: quota.UsageSource(row.UsageSource), Note: row.Note, CreatedBy: row.CreatedBy,
			CreatedAt: row.CreatedAt.Time.UTC(), OwnerName: row.OwnerName, ActorName: row.ActorName,
		})
	}
	return quota.PageResult[quota.LedgerEvent]{Items: items, Total: total}, nil
}

func (r *QuotaRepository) ListRequestLogs(ctx context.Context, query quota.RequestLogQuery) (quota.PageResult[quota.RequestLog], error) {
	params := db.CountRequestLogsParams{
		UserID: query.UserID, GatewayKeyID: query.GatewayKeyID, ModelID: query.ModelID, Status: string(query.Status),
		FromTime: requiredTimestamp(query.From), ToTime: requiredTimestamp(query.To), Search: query.Search, ResourcePoolID: query.ResourcePoolID,
	}
	total, err := r.queries.CountRequestLogs(ctx, params)
	if err != nil {
		return quota.PageResult[quota.RequestLog]{}, translateQuotaError(err)
	}
	rows, err := r.queries.ListRequestLogs(ctx, db.ListRequestLogsParams{
		UserID: params.UserID, GatewayKeyID: params.GatewayKeyID, ModelID: params.ModelID, Status: params.Status,
		FromTime: params.FromTime, ToTime: params.ToTime, Search: params.Search, ResourcePoolID: params.ResourcePoolID,
		PageOffset: query.Page.Offset, PageSize: query.Page.Size,
	})
	if err != nil {
		return quota.PageResult[quota.RequestLog]{}, translateQuotaError(err)
	}
	items := make([]quota.RequestLog, 0, len(rows))
	for _, row := range rows {
		items = append(items, requestLogFromParts(row.ID, row.UserID, row.UserName, row.GatewayKeyID, row.KeyPrefix, row.ModelID, row.ModelAlias, row.ResourcePoolID, row.ResourcePoolName, row.ResourcePoolSlug, row.Status, row.Stream, row.InputTokens, row.OutputTokens, row.UsageSource, row.ErrorKind, row.AcceptedAt, row.CompletedAt, row.UpdatedAt, row.AttemptCount, row.LastAttemptStatus))
	}
	return quota.PageResult[quota.RequestLog]{Items: items, Total: total}, nil
}

func (r *QuotaRepository) GetRequestLog(ctx context.Context, requestID uuid.UUID, userID *uuid.UUID) (quota.RequestLogDetail, error) {
	row, err := r.queries.GetRequestLog(ctx, db.GetRequestLogParams{RequestID: requestID, UserID: userID})
	if err != nil {
		return quota.RequestLogDetail{}, translateQuotaError(err)
	}
	result := quota.RequestLogDetail{RequestLog: requestLogFromParts(row.ID, row.UserID, row.UserName, row.GatewayKeyID, row.KeyPrefix, row.ModelID, row.ModelAlias, row.ResourcePoolID, row.ResourcePoolName, row.ResourcePoolSlug, row.Status, row.Stream, row.InputTokens, row.OutputTokens, row.UsageSource, row.ErrorKind, row.AcceptedAt, row.CompletedAt, row.UpdatedAt, row.AttemptCount, row.LastAttemptStatus)}
	attempts, err := r.queries.ListRequestLogAttempts(ctx, requestID)
	if err != nil {
		return quota.RequestLogDetail{}, translateQuotaError(err)
	}
	for _, attempt := range attempts {
		result.Attempts = append(result.Attempts, quota.RequestAttempt{
			ID: attempt.ID, Sequence: attempt.Sequence, Status: string(attempt.Status), ProviderName: attempt.ProviderName,
			CredentialName: attempt.CredentialName, UpstreamRequestID: attempt.UpstreamRequestID, HTTPStatus: attempt.HttpStatus,
			ErrorKind: attempt.ErrorKind, RetryAfterAt: timePointer(attempt.RetryAfterAt), SentAt: timePointer(attempt.SentAt),
			FirstByteAt: timePointer(attempt.FirstByteAt), CompletedAt: timePointer(attempt.CompletedAt), InputTokens: attempt.InputTokens,
			OutputTokens: attempt.OutputTokens, UsageSource: quota.UsageSource(attempt.UsageSource), CreatedAt: attempt.CreatedAt.Time.UTC(),
		})
	}
	return result, nil
}

func requestLogFromParts(id, userID uuid.UUID, userName string, gatewayKeyID uuid.UUID, keyPrefix string, modelID uuid.UUID, modelAlias string, resourcePoolID uuid.UUID, poolName, poolSlug string, status db.RequestStatus, stream bool, inputTokens, outputTokens *int64, source db.UsageSource, errorKind *string, acceptedAt, completedAt, updatedAt pgtype.Timestamptz, attemptCount int64, lastAttemptStatus string) quota.RequestLog {
	return quota.RequestLog{
		RequestID: id, UserID: userID, UserName: userName, GatewayKeyID: gatewayKeyID, KeyPrefix: keyPrefix,
		ModelID: modelID, ModelAlias: modelAlias, ResourcePoolID: resourcePoolID, ResourcePoolName: poolName, ResourcePoolSlug: poolSlug,
		Status: quota.RequestStatus(status), Stream: stream, InputTokens: inputTokens, OutputTokens: outputTokens, UsageSource: quota.UsageSource(source),
		ErrorKind: errorKind, AcceptedAt: acceptedAt.Time.UTC(), CompletedAt: timePointer(completedAt), UpdatedAt: updatedAt.Time.UTC(),
		AttemptCount: attemptCount, LastAttemptStatus: optionalString(lastAttemptStatus),
	}
}

func requiredTimestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
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
