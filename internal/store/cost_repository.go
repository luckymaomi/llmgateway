package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/luckymaomi/llmgateway/internal/costing"
	db "github.com/luckymaomi/llmgateway/internal/store/db"
)

type CostRepository struct {
	connections *Connections
	queries     *db.Queries
}

func NewCostRepository(connections *Connections) *CostRepository {
	return &CostRepository{connections: connections, queries: db.New(connections.Postgres)}
}

func (r *CostRepository) CreatePriceVersion(ctx context.Context, input costing.NewPriceVersion, mutation costing.MutationRequest, actorID uuid.UUID) (costing.PriceVersion, error) {
	inputRate, err := costing.ParseRate(input.InputPricePerMillionTokens)
	if err != nil {
		return costing.PriceVersion{}, err
	}
	outputRate, err := costing.ParseRate(input.OutputPricePerMillionTokens)
	if err != nil {
		return costing.PriceVersion{}, err
	}
	fingerprint, err := priceFingerprint(input, inputRate, outputRate)
	if err != nil {
		return costing.PriceVersion{}, err
	}
	tx, err := r.connections.Postgres.Begin(ctx)
	if err != nil {
		return costing.PriceVersion{}, translateCostError(err)
	}
	defer tx.Rollback(ctx)
	queries := r.queries.WithTx(tx)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", actorID.String()+":"+mutation.IdempotencyKey.String()); err != nil {
		return costing.PriceVersion{}, translateCostError(err)
	}
	claimed, err := queries.ClaimModelPriceMutation(ctx, db.ClaimModelPriceMutationParams{
		ActorUserID: actorID, IdempotencyKey: mutation.IdempotencyKey, RequestFingerprint: fingerprint, RequestID: mutation.RequestID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		existing, lookupErr := queries.GetModelPriceMutation(ctx, db.GetModelPriceMutationParams{ActorUserID: actorID, IdempotencyKey: mutation.IdempotencyKey})
		if lookupErr != nil {
			return costing.PriceVersion{}, translateCostError(lookupErr)
		}
		if !bytes.Equal(existing.RequestFingerprint, fingerprint) || existing.PriceVersionID == nil {
			return costing.PriceVersion{}, costing.ErrConflict
		}
		row, lookupErr := queries.GetModelPriceVersion(ctx, *existing.PriceVersionID)
		if lookupErr != nil {
			return costing.PriceVersion{}, translateCostError(lookupErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return costing.PriceVersion{}, costing.ErrOutcomeUnknown
		}
		result := priceVersionFromGetRow(row)
		result.Replayed = true
		return result, nil
	}
	if err != nil {
		return costing.PriceVersion{}, translateCostError(err)
	}
	created, err := queries.CreateModelPriceVersion(ctx, db.CreateModelPriceVersionParams{
		ModelID: input.ModelID, Currency: input.Currency, InputRateNanosPerMillion: inputRate,
		OutputRateNanosPerMillion: outputRate, EffectiveAt: pgtype.Timestamptz{Time: input.EffectiveAt, Valid: true}, CreatedBy: actorID,
	})
	if err != nil {
		return costing.PriceVersion{}, translateCostError(err)
	}
	priceID := created.ID
	if _, err := queries.CompleteModelPriceMutation(ctx, db.CompleteModelPriceMutationParams{PriceVersionID: &priceID, ID: claimed.ID}); err != nil {
		return costing.PriceVersion{}, translateCostError(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_events (actor_user_id, action, target_type, target_id, request_id, detail)
		VALUES ($1, 'costing.model_price_created', 'model_price_version', $2, $3, jsonb_build_object('modelId', $4::text, 'currency', $5::text, 'effectiveAt', $6::timestamptz))`,
		actorID, created.ID.String(), mutation.RequestID, input.ModelID.String(), input.Currency, input.EffectiveAt); err != nil {
		return costing.PriceVersion{}, translateCostError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return costing.PriceVersion{}, costing.ErrOutcomeUnknown
	}
	row, err := r.queries.GetModelPriceVersion(ctx, created.ID)
	if err != nil {
		return costing.PriceVersion{}, translateCostError(err)
	}
	return priceVersionFromGetRow(row), nil
}

func (r *CostRepository) ListPriceVersions(ctx context.Context, modelID *uuid.UUID, page costing.Page) ([]costing.PriceVersion, error) {
	rows, err := r.queries.ListModelPriceVersions(ctx, db.ListModelPriceVersionsParams{ModelID: modelID, PageOffset: page.Offset, PageSize: page.Size})
	if err != nil {
		return nil, translateCostError(err)
	}
	result := make([]costing.PriceVersion, 0, len(rows))
	for _, row := range rows {
		result = append(result, priceVersionFromListRow(row))
	}
	return result, nil
}

func (r *CostRepository) ListSummaries(ctx context.Context, page costing.Page) ([]costing.Summary, error) {
	rows, err := r.queries.ListCostSummaries(ctx, db.ListCostSummariesParams{PageOffset: page.Offset, PageSize: page.Size})
	if err != nil {
		return nil, translateCostError(err)
	}
	result := make([]costing.Summary, 0, len(rows))
	for _, row := range rows {
		result = append(result, costing.Summary{
			UserID: row.UserID, UserName: row.UserName, SubscriptionID: row.SubscriptionID, ServicePlanName: row.ServicePlanName, PlanKind: row.PlanKind,
			ModelID: row.ModelID, ModelAlias: row.ModelAlias, ProviderID: row.ProviderID, ProviderName: row.ProviderName,
			ResourcePoolID: row.ResourcePoolID, ResourcePoolName: row.ResourcePoolName, Currency: row.Currency, RequestCount: row.RequestCount,
			InputTokens: row.InputTokens, OutputTokens: row.OutputTokens, InputCostNanos: row.InputCostNanos,
			OutputCostNanos: row.OutputCostNanos, TotalCostNanos: row.TotalCostNanos,
		})
	}
	return result, nil
}

func priceFingerprint(input costing.NewPriceVersion, inputRate, outputRate int64) ([]byte, error) {
	payload, err := json.Marshal(struct {
		ModelID, Currency     string
		InputRate, OutputRate int64
		EffectiveAt           string
	}{input.ModelID.String(), input.Currency, inputRate, outputRate, input.EffectiveAt.UTC().Format("2006-01-02T15:04:05.000000Z07:00")})
	if err != nil {
		return nil, fmt.Errorf("costing fingerprint: %w", err)
	}
	digest := sha256.Sum256(payload)
	return digest[:], nil
}

func priceVersionFromGetRow(row db.GetModelPriceVersionRow) costing.PriceVersion {
	return costing.PriceVersion{ID: row.ID, ModelID: row.ModelID, ModelAlias: row.ModelAlias, Currency: row.Currency,
		InputRateNanosPerMillion: row.InputRateNanosPerMillion, OutputRateNanosPerMillion: row.OutputRateNanosPerMillion,
		EffectiveAt: row.EffectiveAt.Time.UTC(), CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt.Time.UTC()}
}

func priceVersionFromListRow(row db.ListModelPriceVersionsRow) costing.PriceVersion {
	return costing.PriceVersion{ID: row.ID, ModelID: row.ModelID, ModelAlias: row.ModelAlias, Currency: row.Currency,
		InputRateNanosPerMillion: row.InputRateNanosPerMillion, OutputRateNanosPerMillion: row.OutputRateNanosPerMillion,
		EffectiveAt: row.EffectiveAt.Time.UTC(), CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt.Time.UTC()}
}

func translateCostError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return costing.ErrNotFound
	}
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) {
		switch databaseError.Code {
		case "23503":
			return costing.ErrNotFound
		case "23505", "40001", "40P01":
			return costing.ErrConflict
		case "23514", "23502", "22P02":
			return costing.ErrInvalidInput
		}
	}
	return fmt.Errorf("costing store: %w", err)
}
