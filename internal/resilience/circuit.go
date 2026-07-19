package resilience

import (
	"sync"
	"time"
)

type CircuitState string

const (
	CircuitClosed   CircuitState = "closed"
	CircuitOpen     CircuitState = "open"
	CircuitHalfOpen CircuitState = "half_open"
)

type CircuitConfig struct {
	FailureThreshold    int
	SuccessThreshold    int
	OpenDuration        time.Duration
	HalfOpenMaxInFlight int
}

type CircuitSnapshot struct {
	State               CircuitState
	ConsecutiveFailures int
	HalfOpenSuccesses   int
	HalfOpenInFlight    int
	RetryAt             time.Time
}

type PermitResult string

const (
	PermitSucceeded PermitResult = "succeeded"
	PermitFailed    PermitResult = "failed"
	PermitReleased  PermitResult = "released"
)

type AcquireResult struct {
	Allowed bool
	State   CircuitState
	RetryAt time.Time
	Permit  *Permit
}

type Circuit struct {
	mu sync.Mutex

	config CircuitConfig
	clock  Clock
	state  CircuitState

	generation          uint64
	consecutiveFailures int
	halfOpenSuccesses   int
	halfOpenInFlight    int
	retryAt             time.Time
}

type Permit struct {
	mu         sync.Mutex
	completed  bool
	circuit    *Circuit
	generation uint64
	state      CircuitState
}

func NewCircuit(config CircuitConfig, clock Clock) (*Circuit, error) {
	if config.FailureThreshold <= 0 || config.SuccessThreshold <= 0 ||
		config.OpenDuration <= 0 || config.HalfOpenMaxInFlight <= 0 {
		return nil, newError(ErrorInvalidConfiguration, "circuit thresholds, duration, and probe limit must be positive")
	}
	if clock == nil {
		return nil, newError(ErrorInvalidConfiguration, "clock is required")
	}
	return &Circuit{config: config, clock: clock, state: CircuitClosed}, nil
}

func (c *Circuit) Acquire() AcquireResult {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.clock.Now().UTC()
	if c.state == CircuitOpen && !now.Before(c.retryAt) {
		c.toHalfOpenLocked()
	}
	if c.state == CircuitOpen {
		return AcquireResult{State: CircuitOpen, RetryAt: c.retryAt}
	}
	if c.state == CircuitHalfOpen && c.halfOpenInFlight >= c.config.HalfOpenMaxInFlight {
		return AcquireResult{State: CircuitHalfOpen}
	}
	if c.state == CircuitHalfOpen {
		c.halfOpenInFlight++
	}
	permit := &Permit{circuit: c, generation: c.generation, state: c.state}
	return AcquireResult{Allowed: true, State: c.state, Permit: permit}
}

// Complete records one terminal permit outcome. Repeated completion is an
// idempotent no-op and returns false.
func (p *Permit) Complete(result PermitResult) bool {
	if p == nil || p.circuit == nil {
		return false
	}
	if result != PermitSucceeded && result != PermitFailed && result != PermitReleased {
		return false
	}
	p.mu.Lock()
	if p.completed {
		p.mu.Unlock()
		return false
	}
	p.completed = true
	p.mu.Unlock()
	return p.circuit.complete(p.generation, p.state, result)
}

func (c *Circuit) Snapshot() CircuitSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return CircuitSnapshot{
		State: c.state, ConsecutiveFailures: c.consecutiveFailures,
		HalfOpenSuccesses: c.halfOpenSuccesses, HalfOpenInFlight: c.halfOpenInFlight, RetryAt: c.retryAt,
	}
}

func (c *Circuit) complete(generation uint64, permitState CircuitState, result PermitResult) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if generation != c.generation || permitState != c.state {
		return false
	}
	switch c.state {
	case CircuitClosed:
		switch result {
		case PermitSucceeded:
			c.consecutiveFailures = 0
		case PermitFailed:
			c.consecutiveFailures++
			if c.consecutiveFailures >= c.config.FailureThreshold {
				c.toOpenLocked(c.clock.Now().UTC())
			}
		case PermitReleased:
		}
	case CircuitHalfOpen:
		if c.halfOpenInFlight > 0 {
			c.halfOpenInFlight--
		}
		switch result {
		case PermitSucceeded:
			c.halfOpenSuccesses++
			if c.halfOpenSuccesses >= c.config.SuccessThreshold {
				c.toClosedLocked()
			}
		case PermitFailed:
			c.toOpenLocked(c.clock.Now().UTC())
		case PermitReleased:
		}
	default:
		return false
	}
	return true
}

func (c *Circuit) toOpenLocked(now time.Time) {
	c.state = CircuitOpen
	c.generation++
	c.consecutiveFailures = 0
	c.halfOpenSuccesses = 0
	c.halfOpenInFlight = 0
	c.retryAt = now.Add(c.config.OpenDuration)
}

func (c *Circuit) toHalfOpenLocked() {
	c.state = CircuitHalfOpen
	c.generation++
	c.halfOpenSuccesses = 0
	c.halfOpenInFlight = 0
	c.retryAt = time.Time{}
}

func (c *Circuit) toClosedLocked() {
	c.state = CircuitClosed
	c.generation++
	c.consecutiveFailures = 0
	c.halfOpenSuccesses = 0
	c.halfOpenInFlight = 0
	c.retryAt = time.Time{}
}
