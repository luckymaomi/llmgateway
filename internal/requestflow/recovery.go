package requestflow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/luckymaomi/llmgateway/internal/execution"
)

func (s *Service) RecoverOnce(ctx context.Context, staleBefore time.Time, batchSize int32) (RecoveryResult, error) {
	if staleBefore.IsZero() || batchSize < 1 || batchSize > 1000 {
		return RecoveryResult{}, fmt.Errorf("invalid request recovery input")
	}
	var result RecoveryResult
	var recoveryErrors []error

	settlements, err := s.repository.ListRecoverableSettlements(ctx, staleBefore, batchSize)
	if err != nil {
		return result, fmt.Errorf("list recoverable settlements: %w", err)
	}
	for _, settlement := range settlements {
		if err := s.accounting.Settle(ctx, settlement.Claim, settlement.Usage); err != nil {
			if !errors.Is(err, execution.ErrFenced) {
				recoveryErrors = append(recoveryErrors, fmt.Errorf("settle request %s: %w", settlement.Claim.RequestID, err))
			}
			continue
		}
		result.Settled++
	}

	queuedRequests, err := s.repository.ListStaleQueuedRequests(ctx, staleBefore, batchSize)
	if err != nil {
		return result, errors.Join(append(recoveryErrors, fmt.Errorf("list stale queued requests: %w", err))...)
	}
	for _, requestID := range queuedRequests {
		if err := s.accounting.ReleaseAccepted(ctx, requestID, "execution_abandoned", "request was accepted but no execution claimed it"); err != nil {
			if !errors.Is(err, execution.ErrFenced) {
				recoveryErrors = append(recoveryErrors, fmt.Errorf("release queued request %s: %w", requestID, err))
			}
			continue
		}
		result.Released++
	}

	result.Uncertain, err = s.repository.RecoverStaleExecutions(ctx, staleBefore, batchSize)
	if err != nil {
		recoveryErrors = append(recoveryErrors, fmt.Errorf("fence stale executions: %w", err))
	}
	return result, errors.Join(recoveryErrors...)
}
