package resilience

import "time"

type RetryConfig struct {
	MaxAttempts int
	MaxElapsed  time.Duration
	Backoff     BackoffConfig
}

type RetryPolicy struct {
	config RetryConfig
	clock  Clock
	random Random
}

func NewRetryPolicy(config RetryConfig, clock Clock, random Random) (*RetryPolicy, error) {
	if config.MaxAttempts <= 0 || config.MaxAttempts > 1000 {
		return nil, newError(ErrorInvalidConfiguration, "max attempts must be between one and one thousand")
	}
	if config.MaxElapsed <= 0 {
		return nil, newError(ErrorInvalidConfiguration, "max elapsed must be positive")
	}
	if clock == nil {
		return nil, newError(ErrorInvalidConfiguration, "clock is required")
	}
	if err := config.Backoff.validate(random); err != nil {
		return nil, err
	}
	return &RetryPolicy{config: config, clock: clock, random: random}, nil
}

func (p *RetryPolicy) Decide(input RetryInput) (RetryDecision, error) {
	now := p.clock.Now().UTC()
	if err := validateRetryInput(input, now); err != nil {
		return RetryDecision{}, err
	}
	if input.ClientBoundary == ClientCommitted {
		return stopRetry(ReasonClientCommitted), nil
	}
	if input.Failure == FailureCanceled {
		return stopRetry(ReasonCanceled), nil
	}
	if (input.Failure == FailureUncertain || input.SendBoundary == SendUncertain) && !input.IdempotencyGuaranteed {
		return RetryDecision{Action: RetryUncertain, Reason: ReasonSendUncertain}, nil
	}
	if !retryableFailure(input.Failure) {
		return stopRetry(ReasonTerminalFailure), nil
	}
	if input.Attempt >= p.config.MaxAttempts {
		return stopRetry(ReasonAttemptBudget), nil
	}
	deadline := input.RequestStartedAt.Add(p.config.MaxElapsed)
	if !now.Before(deadline) {
		return stopRetry(ReasonElapsedBudget), nil
	}
	delay, err := backoffDelay(p.config.Backoff, input.Attempt, p.random)
	if err != nil {
		return RetryDecision{}, err
	}
	if input.RetryAfter != nil {
		retryAfterDelay, retryAfterError := input.RetryAfter.delay(now)
		if retryAfterError != nil {
			return RetryDecision{}, retryAfterError
		}
		if retryAfterDelay > delay {
			delay = retryAfterDelay
		}
	}
	nextAttemptAt := now.Add(delay)
	if !nextAttemptAt.Before(deadline) {
		return stopRetry(ReasonElapsedBudget), nil
	}
	return RetryDecision{
		Action: RetrySchedule, Reason: ReasonRetryableFailure, NextAttempt: input.Attempt + 1,
		Delay: delay, NextAttemptAt: nextAttemptAt,
	}, nil
}

func validateRetryInput(input RetryInput, now time.Time) error {
	if input.Attempt <= 0 || input.RequestStartedAt.IsZero() || now.Before(input.RequestStartedAt) {
		return newError(ErrorInvalidInput, "attempt and request start time are invalid")
	}
	if !validFailure(input.Failure) || !validSendBoundary(input.SendBoundary) || !validClientBoundary(input.ClientBoundary) {
		return newError(ErrorInvalidInput, "retry input contains an invalid state")
	}
	return nil
}

func (retryAfter RetryAfter) delay(now time.Time) (time.Duration, error) {
	if retryAfter.Delay < 0 || (!retryAfter.At.IsZero() && retryAfter.Delay != 0) {
		return 0, newError(ErrorInvalidInput, "retry-after must contain one non-negative delay or deadline")
	}
	if !retryAfter.At.IsZero() {
		if !retryAfter.At.After(now) {
			return 0, nil
		}
		return retryAfter.At.Sub(now), nil
	}
	return retryAfter.Delay, nil
}

func retryableFailure(failure FailureClass) bool {
	switch failure {
	case FailureRateLimit, FailureProviderTemporary, FailureTimeout, FailureNetwork,
		FailureStreamInterrupted, FailureUncertain:
		return true
	default:
		return false
	}
}

func validFailure(failure FailureClass) bool {
	switch failure {
	case FailureInvalidRequest, FailureAuthentication, FailurePermission, FailureQuota,
		FailureRateLimit, FailureProviderTemporary, FailureProviderPermanent, FailureTimeout,
		FailureNetwork, FailureCanceled, FailureStreamInterrupted, FailureStorage, FailureUncertain:
		return true
	default:
		return false
	}
}

func validSendBoundary(boundary SendBoundary) bool {
	return boundary == SendNotStarted || boundary == SendRejected || boundary == SendAccepted || boundary == SendUncertain
}

func validClientBoundary(boundary ClientBoundary) bool {
	return boundary == ClientUncommitted || boundary == ClientCommitted
}

func stopRetry(reason RetryReason) RetryDecision {
	return RetryDecision{Action: RetryStop, Reason: reason}
}
