package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/registry"
	"github.com/luckymaomi/llmgateway/internal/requestflow"
	"github.com/luckymaomi/llmgateway/internal/store/db"
)

type RequestRepository struct {
	queries *db.Queries
}

func NewRequestRepository(connections *Connections) *RequestRepository {
	return &RequestRepository{queries: db.New(connections.Postgres)}
}

func (r *RequestRepository) ListAuthorizedModels(ctx context.Context, userID uuid.UUID) ([]requestflow.Model, error) {
	rows, err := r.queries.ListAuthorizedModels(ctx, userID)
	if err != nil {
		return nil, err
	}
	models := make([]requestflow.Model, 0, len(rows))
	for _, row := range rows {
		provider, err := r.queries.GetProvider(ctx, row.ProviderID)
		if err != nil {
			return nil, err
		}
		capabilities, err := decodeCapabilities(row.Capabilities)
		if err != nil {
			return nil, fmt.Errorf("decode capabilities for model %s: %w", row.ID, err)
		}
		models = append(models, requestflow.Model{
			ID: row.ID, PublicName: row.PublicName, UpstreamName: row.UpstreamName,
			ProviderID: row.ProviderID, ProviderSlug: provider.Slug, ProviderKind: providers.Kind(provider.Kind), ProviderBaseURL: provider.BaseUrl,
			ResourceDomain: registry.ResourceDomain(row.ResourceDomain), Capabilities: capabilities, CreatedAt: row.CreatedAt.Time,
		})
	}
	return models, nil
}

func (r *RequestRepository) ResolveAuthorizedModel(ctx context.Context, userID uuid.UUID, publicName string) (requestflow.Model, error) {
	row, err := r.queries.GetModelByPublicName(ctx, publicName)
	if errors.Is(err, pgx.ErrNoRows) || !row.Enabled || !row.ProviderEnabled {
		return requestflow.Model{}, requestflow.ErrModelNotFound
	}
	if err != nil {
		return requestflow.Model{}, err
	}
	authorized, err := r.queries.IsUserAuthorizedForModel(ctx, db.IsUserAuthorizedForModelParams{UserID: userID, ModelID: row.ID})
	if err != nil {
		return requestflow.Model{}, err
	}
	if !authorized {
		return requestflow.Model{}, requestflow.ErrModelNotAuthorized
	}
	capabilities, err := decodeCapabilities(row.Capabilities)
	if err != nil {
		return requestflow.Model{}, fmt.Errorf("decode model capabilities: %w", err)
	}
	return requestflow.Model{
		ID: row.ID, PublicName: row.PublicName, UpstreamName: row.UpstreamName,
		ProviderID: row.ProviderID, ProviderSlug: row.ProviderSlug, ProviderKind: providers.Kind(row.ProviderKind), ProviderBaseURL: row.ProviderBaseUrl,
		ResourceDomain: registry.ResourceDomain(row.ResourceDomain), Capabilities: capabilities, CreatedAt: row.CreatedAt.Time,
	}, nil
}

func (r *RequestRepository) ListCandidates(ctx context.Context, modelID uuid.UUID, domain registry.ResourceDomain) ([]requestflow.Candidate, error) {
	rows, err := r.queries.ListEligibleCredentials(ctx, db.ListEligibleCredentialsParams{ModelID: modelID, ResourceDomain: db.ResourceDomain(domain)})
	if err != nil {
		return nil, err
	}
	candidates := make([]requestflow.Candidate, 0, len(rows))
	for _, row := range rows {
		candidates = append(candidates, requestflow.Candidate{
			ID: row.ID, Priority: row.Priority, Weight: row.Weight, RPMLimit: row.RpmLimit, TPMLimit: row.TpmLimit,
			ConcurrencyLimit: row.ConcurrencyLimit, FixedProxyURL: row.FixedProxyUrl,
			ConsecutiveFailures: row.ConsecutiveFailures, LastSuccessAt: timePointer(row.LastSuccessAt), CooldownUntil: timePointer(row.CooldownUntil),
		})
	}
	return candidates, nil
}

func (r *RequestRepository) ActiveConfigRevision(ctx context.Context) (*uuid.UUID, error) {
	active, err := r.queries.GetActiveConfig(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &active.ID, nil
}

func (r *RequestRepository) CreateAttempt(ctx context.Context, requestID, credentialID uuid.UUID, sequence int) (uuid.UUID, error) {
	attempt, err := r.queries.CreateAttempt(ctx, db.CreateAttemptParams{
		RequestID: requestID, CredentialID: credentialID, Sequence: int32(sequence), Status: db.AttemptStatusCreated,
	})
	return attempt.ID, err
}

func (r *RequestRepository) UpdateAttempt(ctx context.Context, attemptID uuid.UUID, update requestflow.AttemptUpdate) error {
	status, err := attemptStatus(update.Status)
	if err != nil {
		return err
	}
	var httpStatus *int32
	if update.HTTPStatus != nil {
		value := int32(*update.HTTPStatus)
		httpStatus = &value
	}
	_, err = r.queries.UpdateAttempt(ctx, db.UpdateAttemptParams{
		ID: attemptID, Status: status, HttpStatus: httpStatus, UpstreamRequestID: update.UpstreamRequestID,
		ErrorKind: update.ErrorKind, RetryAfterAt: optionalTimestamp(update.RetryAfterAt), SentAt: optionalTimestamp(update.SentAt),
		FirstByteAt: optionalTimestamp(update.FirstByteAt), CompletedAt: optionalTimestamp(update.CompletedAt),
	})
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
