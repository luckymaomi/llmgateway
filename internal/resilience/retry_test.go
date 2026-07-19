package resilience

import (
	"sync"
	"testing"
	"time"
)

type manualClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualClock) Advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	c.mu.Unlock()
}

type upperBoundRandom struct{}

func (upperBoundRandom) Int63n(limit int64) int64 { return limit - 1 }

func newTestRetryPolicy(t *testing.T, clock *manualClock, maxAttempts int, maxElapsed time.Duration) *RetryPolicy {
	t.Helper()
	policy, err := NewRetryPolicy(RetryConfig{
		MaxAttempts: maxAttempts,
		MaxElapsed:  maxElapsed,
		Backoff: BackoffConfig{
			Initial: 100 * time.Millisecond, Maximum: 2 * time.Second,
			MultiplierNumerator: 2, MultiplierDenominator: 1,
		},
	}, clock, nil)
	if err != nil {
		t.Fatalf("NewRetryPolicy() error = %v", err)
	}
	return policy
}

func retryableInput(clock *manualClock) RetryInput {
	return RetryInput{
		Attempt: 1, RequestStartedAt: clock.Now(), Failure: FailureProviderTemporary,
		SendBoundary: SendRejected, ClientBoundary: ClientUncommitted,
	}
}

func TestRetryPolicySchedulesTemporaryFailureInsideTotalBudget(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)}
	policy := newTestRetryPolicy(t, clock, 3, 10*time.Second)

	decision, err := policy.Decide(retryableInput(clock))
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if decision.Action != RetrySchedule || decision.NextAttempt != 2 || decision.Delay != 100*time.Millisecond {
		t.Fatalf("decision = %#v", decision)
	}
	if !decision.NextAttemptAt.Equal(clock.Now().Add(100 * time.Millisecond)) {
		t.Fatalf("next attempt at = %v", decision.NextAttemptAt)
	}
}

func TestRetryPolicyRespectsRetryAfter(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)}
	policy := newTestRetryPolicy(t, clock, 3, 10*time.Second)
	input := retryableInput(clock)
	input.Failure = FailureRateLimit
	input.RetryAfter = &RetryAfter{At: clock.Now().Add(3 * time.Second)}

	decision, err := policy.Decide(input)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if decision.Action != RetrySchedule || decision.Delay != 3*time.Second {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestRetryPolicyPreservesUnknownSendBoundary(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)}
	policy := newTestRetryPolicy(t, clock, 3, 10*time.Second)
	input := retryableInput(clock)
	input.SendBoundary = SendUncertain

	decision, err := policy.Decide(input)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if decision.Action != RetryUncertain || decision.Reason != ReasonSendUncertain {
		t.Fatalf("decision = %#v", decision)
	}

	input.IdempotencyGuaranteed = true
	decision, err = policy.Decide(input)
	if err != nil {
		t.Fatalf("Decide() with idempotency error = %v", err)
	}
	if decision.Action != RetrySchedule {
		t.Fatalf("idempotent decision = %#v", decision)
	}
}

func TestRetryPolicyProtectsCommittedStream(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)}
	policy := newTestRetryPolicy(t, clock, 3, 10*time.Second)
	input := retryableInput(clock)
	input.ClientBoundary = ClientCommitted
	input.Failure = FailureStreamInterrupted

	decision, err := policy.Decide(input)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if decision.Action != RetryStop || decision.Reason != ReasonClientCommitted {
		t.Fatalf("decision = %#v", decision)
	}

	input.ClientBoundary = ClientUncommitted
	input.SendBoundary = SendAccepted
	decision, err = policy.Decide(input)
	if err != nil {
		t.Fatalf("Decide() before commit error = %v", err)
	}
	if decision.Action != RetrySchedule {
		t.Fatalf("pre-commit decision = %#v", decision)
	}
}

func TestRetryPolicyStopsAtAttemptAndElapsedBudgets(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)}
	policy := newTestRetryPolicy(t, clock, 2, time.Second)
	input := retryableInput(clock)
	input.Attempt = 2
	decision, err := policy.Decide(input)
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	if decision.Action != RetryStop || decision.Reason != ReasonAttemptBudget {
		t.Fatalf("attempt decision = %#v", decision)
	}

	input.Attempt = 1
	input.RetryAfter = &RetryAfter{Delay: 2 * time.Second}
	decision, err = policy.Decide(input)
	if err != nil {
		t.Fatalf("Decide() with Retry-After error = %v", err)
	}
	if decision.Action != RetryStop || decision.Reason != ReasonElapsedBudget {
		t.Fatalf("elapsed decision = %#v", decision)
	}
}

func TestBackoffUsesInjectedJitterWithinConfiguredBounds(t *testing.T) {
	config := BackoffConfig{
		Initial: 100 * time.Millisecond, Maximum: time.Second,
		MultiplierNumerator: 2, MultiplierDenominator: 1, JitterPermille: 200,
	}
	if err := config.validate(upperBoundRandom{}); err != nil {
		t.Fatalf("validate() error = %v", err)
	}
	delay, err := backoffDelay(config, 1, upperBoundRandom{})
	if err != nil {
		t.Fatalf("backoffDelay() error = %v", err)
	}
	if delay != 120*time.Millisecond {
		t.Fatalf("delay = %v, want 120ms", delay)
	}
	delay, err = backoffDelay(config, 8, upperBoundRandom{})
	if err != nil {
		t.Fatalf("backoffDelay(max) error = %v", err)
	}
	if delay != time.Second {
		t.Fatalf("bounded delay = %v, want 1s", delay)
	}
}
