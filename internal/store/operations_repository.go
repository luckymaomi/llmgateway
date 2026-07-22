package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/operations"
	db "github.com/luckymaomi/llmgateway/internal/store/db"
)

type OperationsRepository struct {
	queries *db.Queries
}

func NewOperationsRepository(connections *Connections) *OperationsRepository {
	return &OperationsRepository{queries: db.New(connections.Postgres)}
}

func (r *OperationsRepository) AdministratorResources(ctx context.Context, observedAt time.Time) (operations.AdministratorResources, error) {
	row, err := r.queries.GetAdministratorResourceSummary(ctx, timestamp(observedAt))
	if err != nil {
		return operations.AdministratorResources{}, err
	}
	return operations.AdministratorResources{
		ProviderCount: row.ProviderCount, EnabledProviderCount: row.EnabledProviderCount, ModelCount: row.ModelCount,
		CredentialCount: row.CredentialCount, ActiveCredentialCount: row.ActiveCredentialCount, CoolingCredentialCount: row.CoolingCredentialCount,
		ActiveMemberCount: row.ActiveMemberCount, PendingMemberCount: row.PendingMemberCount, ActiveGatewayKeyCount: row.ActiveGatewayKeyCount,
		ActiveEntitlementCount: row.ActiveEntitlementCount, HasActiveConfiguration: row.HasActiveConfiguration, HasModelPrice: row.HasModelPrice,
	}, nil
}

func (r *OperationsRepository) MemberAccess(ctx context.Context, userID uuid.UUID, observedAt time.Time) (operations.MemberAccess, error) {
	row, err := r.queries.GetMemberAccessSummary(ctx, db.GetMemberAccessSummaryParams{UserID: userID, ObservedAt: timestamp(observedAt)})
	if err != nil {
		return operations.MemberAccess{}, err
	}
	return operations.MemberAccess{
		ActiveGatewayKeyCount: row.ActiveGatewayKeyCount, ActiveEntitlementCount: row.ActiveEntitlementCount,
		RemainingTokens: row.RemainingTokens, NearestEntitlementExpiry: timePointer(row.NearestEntitlementExpiry),
	}, nil
}

func (r *OperationsRepository) RequestSummary(ctx context.Context, userID *uuid.UUID, since, until time.Time) (operations.RequestSummary, error) {
	window := db.GetRequestWindowSummaryParams{Since: timestamp(since), Until: timestamp(until), UserID: userID}
	row, err := r.queries.GetRequestWindowSummary(ctx, window)
	if err != nil {
		return operations.RequestSummary{}, err
	}
	latency, err := r.queries.GetAttemptLatencySummary(ctx, db.GetAttemptLatencySummaryParams(window))
	if err != nil {
		return operations.RequestSummary{}, err
	}
	return operations.RequestSummary{
		RequestCount: row.RequestCount, CompletedCount: row.CompletedCount, FailedCount: row.FailedCount, UncertainCount: row.UncertainCount,
		InputTokens: row.InputTokens, OutputTokens: row.OutputTokens, FirstByteP95Ms: latency.FirstByteP95Ms, TotalLatencyP95Ms: latency.TotalP95Ms,
	}, nil
}

func (r *OperationsRepository) RequestTrend(ctx context.Context, userID *uuid.UUID, since, until time.Time) ([]operations.TrendPoint, error) {
	rows, err := r.queries.ListRequestTrend(ctx, db.ListRequestTrendParams{Since: timestamp(since), Until: timestamp(until), UserID: userID})
	if err != nil {
		return nil, err
	}
	items := make([]operations.TrendPoint, 0, len(rows))
	for _, row := range rows {
		items = append(items, operations.TrendPoint{Bucket: row.Bucket.Time.UTC(), RequestCount: row.RequestCount, InputTokens: row.InputTokens, OutputTokens: row.OutputTokens})
	}
	return items, nil
}

func (r *OperationsRepository) RequestErrors(ctx context.Context, userID *uuid.UUID, since, until time.Time) ([]operations.ErrorCount, error) {
	rows, err := r.queries.ListRequestErrors(ctx, db.ListRequestErrorsParams{Since: timestamp(since), Until: timestamp(until), UserID: userID})
	if err != nil {
		return nil, err
	}
	items := make([]operations.ErrorCount, 0, len(rows))
	for _, row := range rows {
		items = append(items, operations.ErrorCount{Kind: row.ErrorKind, Count: row.RequestCount})
	}
	return items, nil
}
