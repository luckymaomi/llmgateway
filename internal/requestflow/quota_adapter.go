package requestflow

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/canonical"
	"github.com/luckymaomi/llmgateway/internal/execution"
	"github.com/luckymaomi/llmgateway/internal/quota"
)

type QuotaAdapter struct {
	service *quota.Service
}

func NewQuotaAdapter(service *quota.Service) (*QuotaAdapter, error) {
	if service == nil {
		return nil, errors.New("quota service is required")
	}
	return &QuotaAdapter{service: service}, nil
}

func (a *QuotaAdapter) AcceptRequest(ctx context.Context, command AcceptCommand) (Accepted, error) {
	accepted, err := a.service.AcceptRequest(ctx, quota.AcceptInput{
		RequestID: command.RequestID,
		UserID:    command.UserID, GatewayKeyID: command.GatewayKeyID, ModelID: command.ModelID,
		ConfigRevisionID: command.ConfigRevisionID, ResourceDomain: quota.ResourceDomain(command.ResourceDomain),
		Stream: command.Stream, RequestDigest: command.RequestDigest, IdempotencyKey: command.IdempotencyKey,
		ReservedTokens: command.ReservedTokens,
	})
	if err != nil {
		return Accepted{}, accountingError(err)
	}
	return Accepted{
		RequestID: accepted.Request.ID, ReservationID: accepted.Reservation.ID,
		EntitlementID:          accepted.EntitlementCapacity.ID,
		EntitlementConcurrency: accepted.EntitlementCapacity.ConcurrencyLimit,
		EntitlementRPMLimit:    accepted.EntitlementCapacity.RPMLimit,
		EntitlementTPMLimit:    accepted.EntitlementCapacity.TPMLimit,
		Existing:               accepted.Replayed,
	}, nil
}

func (a *QuotaAdapter) Settle(ctx context.Context, claim execution.Claim, usage Usage) error {
	_, err := a.service.Settle(ctx, claim.RequestID, claim, usage.InputTokens, usage.OutputTokens, quota.UsageSource(usage.Source))
	return accountingError(err)
}

func (a *QuotaAdapter) Release(ctx context.Context, claim execution.Claim, errorKind, errorDetail string) error {
	_, err := a.service.Release(ctx, claim.RequestID, claim, errorKind, errorDetail)
	return accountingError(err)
}

func (a *QuotaAdapter) ReleaseAccepted(ctx context.Context, requestID uuid.UUID, errorKind, errorDetail string) error {
	_, err := a.service.ReleaseAccepted(ctx, requestID, errorKind, errorDetail)
	return accountingError(err)
}

func (a *QuotaAdapter) Compensate(ctx context.Context, claim execution.Claim, usage Usage, detail string) error {
	_, err := a.service.Compensate(ctx, claim.RequestID, claim, usage.InputTokens, usage.OutputTokens, quota.UsageSource(usage.Source), string(canonical.ErrorUncertain), detail)
	return accountingError(err)
}

func accountingError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, quota.ErrQuotaExhausted):
		return ErrQuotaExhausted
	case errors.Is(err, quota.ErrModelNotAuthorized):
		return ErrModelNotAuthorized
	case errors.Is(err, quota.ErrConflict):
		return ErrIdempotencyConflict
	case errors.Is(err, quota.ErrInvalidInput), errors.Is(err, quota.ErrUsageUnknown):
		return ErrInvalidAccounting
	default:
		return err
	}
}
