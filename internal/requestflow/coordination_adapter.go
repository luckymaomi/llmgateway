package requestflow

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/admission"
	"github.com/luckymaomi/llmgateway/internal/coordination"
)

type Capacity struct {
	RequestsPerMinute int64
	TokensPerMinute   int64
	Concurrency       int64
}

type CoordinationConfig struct {
	Global            Capacity
	ResourceDomain    Capacity
	User              Capacity
	GatewayKey        Capacity
	Model             Capacity
	Provider          Capacity
	DefaultCredential Capacity
	LeaseTTL          time.Duration
	RetryInterval     time.Duration
}

type CoordinationAdapter struct {
	coordinator *coordination.Coordinator
	config      CoordinationConfig
}

type AdmissionAdapter struct {
	gate        *admission.Gate
	coordinator *coordination.Coordinator
	config      AdmissionCoordinationConfig
}

type AdmissionCoordinationConfig struct {
	MaxActive        int64
	MaxActivePerUser int64
	MaxQueueWait     time.Duration
	RetryInterval    time.Duration
	LeaseTTL         time.Duration
}

type CapacityError struct {
	RetryAt time.Time
}

func (e *CapacityError) Error() string {
	return "request capacity is temporarily exhausted"
}

func NewAdmissionAdapter(gate *admission.Gate, coordinator *coordination.Coordinator, config AdmissionCoordinationConfig) (*AdmissionAdapter, error) {
	if gate == nil || coordinator == nil || config.MaxActive < 1 || config.MaxActivePerUser < 1 || config.MaxActivePerUser > config.MaxActive ||
		config.MaxQueueWait <= 0 || config.RetryInterval < 10*time.Millisecond || config.RetryInterval > time.Second ||
		config.LeaseTTL < 3*time.Second || config.LeaseTTL > time.Hour {
		return nil, errors.New("admission coordination configuration is invalid")
	}
	return &AdmissionAdapter{gate: gate, coordinator: coordinator, config: config}, nil
}

func (a *AdmissionAdapter) Acquire(ctx context.Context, request AdmissionRequest) (AdmissionPermit, time.Duration, error) {
	if request.RequestID == uuid.Nil || request.UserID == uuid.Nil {
		return nil, 0, fmt.Errorf("%w: admission identity is required", ErrCoordinationFailed)
	}
	startedAt := time.Now()
	capacityWaitDeadline := startedAt.Add(a.config.MaxQueueWait)
	waitContext, cancel := context.WithDeadline(ctx, capacityWaitDeadline)
	defer cancel()
	if deadline, ok := waitContext.Deadline(); ok {
		capacityWaitDeadline = deadline
	}
	for {
		localPermit, _, err := a.gate.Acquire(waitContext, admission.Request{
			ID: admission.TicketID(request.RequestID.String()), UserID: admission.UserID(request.UserID.String()),
		})
		if err != nil {
			if errors.Is(waitContext.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
				return nil, time.Since(startedAt), fmt.Errorf("%w: shared admission wait timed out", ErrAdmissionTimedOut)
			}
			return nil, time.Since(startedAt), admissionGateError(err)
		}
		decision, err := a.coordinator.AcquireLease(waitContext, request.RequestID.String(), a.config.LeaseTTL, []coordination.ConcurrencyLimit{
			{Dimension: coordination.GlobalDimension(), MaxInFlight: a.config.MaxActive},
			{Dimension: coordination.Dimension{Scope: coordination.ScopeUser, SubjectID: request.UserID.String()}, MaxInFlight: a.config.MaxActivePerUser},
		})
		if err != nil {
			localPermit.Release()
			return nil, time.Since(startedAt), fmt.Errorf("%w: acquire shared admission: %v", ErrCoordinationFailed, err)
		}
		if decision.Granted {
			permit := &sharedAdmissionPermit{
				local: localPermit, coordinator: a.coordinator, reference: decision.Lease,
				ttl: a.config.LeaseTTL, capacityWaitDeadline: capacityWaitDeadline,
				done: make(chan struct{}), stopped: make(chan struct{}),
			}
			go permit.renew()
			return permit, time.Since(startedAt), nil
		}
		localPermit.Release()
		delay := a.config.RetryInterval
		if untilRetry := time.Until(decision.RetryAt); untilRetry > 0 && untilRetry < delay {
			delay = untilRetry
		}
		timer := time.NewTimer(delay)
		select {
		case <-waitContext.Done():
			timer.Stop()
			if errors.Is(waitContext.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
				return nil, time.Since(startedAt), fmt.Errorf("%w: shared admission wait timed out", ErrAdmissionTimedOut)
			}
			return nil, time.Since(startedAt), fmt.Errorf("%w: %v", ErrAdmissionCanceled, waitContext.Err())
		case <-timer.C:
		}
	}
}

type sharedAdmissionPermit struct {
	local                *admission.Permit
	coordinator          *coordination.Coordinator
	reference            coordination.LeaseRef
	ttl                  time.Duration
	capacityWaitDeadline time.Time
	done                 chan struct{}
	stopped              chan struct{}
	once                 sync.Once
}

func (p *sharedAdmissionPermit) CapacityWaitDeadline() time.Time {
	if p == nil {
		return time.Time{}
	}
	return p.capacityWaitDeadline
}

func (p *sharedAdmissionPermit) Release() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		close(p.done)
		<-p.stopped
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = p.coordinator.ReleaseLease(ctx, p.reference)
		p.local.Release()
	})
}

func (p *sharedAdmissionPermit) renew() {
	defer close(p.stopped)
	ticker := time.NewTicker(p.ttl / 3)
	defer ticker.Stop()
	for {
		select {
		case <-p.done:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), p.ttl/3)
			_, err := p.coordinator.RenewLease(ctx, p.reference, p.ttl)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func NewCoordinationAdapter(coordinator *coordination.Coordinator, config CoordinationConfig) (*CoordinationAdapter, error) {
	if coordinator == nil || config.LeaseTTL < 3*time.Second || config.LeaseTTL > time.Hour ||
		config.RetryInterval < 10*time.Millisecond || config.RetryInterval > time.Second {
		return nil, errors.New("coordination adapter configuration is invalid")
	}
	for _, capacity := range []Capacity{config.Global, config.ResourceDomain, config.User, config.GatewayKey, config.Model, config.Provider, config.DefaultCredential} {
		if capacity.RequestsPerMinute < 1 || capacity.TokensPerMinute < 1 || capacity.Concurrency < 1 {
			return nil, errors.New("all coordination capacity defaults must be positive")
		}
	}
	return &CoordinationAdapter{coordinator: coordinator, config: config}, nil
}

func (a *CoordinationAdapter) Acquire(ctx context.Context, request LeaseRequest) (Lease, time.Duration, error) {
	if request.ExecutionID == uuid.Nil {
		return nil, 0, fmt.Errorf("%w: execution id is required", ErrCoordinationFailed)
	}
	dimensions := requestDimensions(request)
	credentialCapacity := a.config.DefaultCredential
	if request.RPMLimit != nil {
		credentialCapacity.RequestsPerMinute = int64(*request.RPMLimit)
	}
	if request.TPMLimit != nil {
		credentialCapacity.TokensPerMinute = *request.TPMLimit
	}
	if request.Concurrency != nil {
		credentialCapacity.Concurrency = int64(*request.Concurrency)
	}
	capacities := []Capacity{a.config.Global, a.config.ResourceDomain, a.config.User, a.config.GatewayKey, a.config.Model, a.config.Provider, credentialCapacity}
	concurrencyLimits := make([]coordination.ConcurrencyLimit, len(dimensions), len(dimensions)+1)
	for index := range dimensions {
		concurrencyLimits[index] = coordination.ConcurrencyLimit{Dimension: dimensions[index], MaxInFlight: capacities[index].Concurrency}
	}
	if request.EntitlementID != uuid.Nil && request.EntitlementConcurrency > 0 {
		concurrencyLimits = append(concurrencyLimits, coordination.ConcurrencyLimit{
			Dimension:   coordination.Dimension{Scope: coordination.ScopeEntitlement, SubjectID: request.EntitlementID.String()},
			MaxInFlight: int64(request.EntitlementConcurrency),
		})
	}
	leaseDecision, wait, err := a.acquireConcurrency(ctx, request.ExecutionID.String(), concurrencyLimits, request.CapacityWaitDeadline)
	if err != nil {
		return nil, wait, err
	}
	if !leaseDecision.Granted {
		return nil, wait, &CapacityError{RetryAt: leaseDecision.RetryAt}
	}

	rateLimits := make([]coordination.BucketLimit, 0, len(dimensions)*2+2)
	for index, dimension := range dimensions {
		capacity := capacities[index]
		rateLimits = append(rateLimits,
			minuteBucket(dimension, coordination.MetricRequests, capacity.RequestsPerMinute, 1),
			minuteBucket(dimension, coordination.MetricTokens, capacity.TokensPerMinute, request.EstimatedTokens),
		)
	}
	if request.EntitlementID != uuid.Nil {
		entitlementDimension := coordination.Dimension{Scope: coordination.ScopeEntitlement, SubjectID: request.EntitlementID.String()}
		if request.EntitlementRPMLimit != nil {
			rateLimits = append(rateLimits, minuteBucket(entitlementDimension, coordination.MetricRequests, int64(*request.EntitlementRPMLimit), 1))
		}
		if request.EntitlementTPMLimit != nil {
			rateLimits = append(rateLimits, minuteBucket(entitlementDimension, coordination.MetricTokens, *request.EntitlementTPMLimit, request.EstimatedTokens))
		}
	}
	rateDecision, err := a.coordinator.AcquireRate(ctx, rateLimits)
	if err != nil {
		_ = a.coordinator.ReleaseLease(context.WithoutCancel(ctx), leaseDecision.Lease)
		return nil, wait, fmt.Errorf("%w: acquire rate: %v", ErrCoordinationFailed, err)
	}
	if !rateDecision.Granted {
		_ = a.coordinator.ReleaseLease(context.WithoutCancel(ctx), leaseDecision.Lease)
		return nil, wait, &CapacityError{RetryAt: rateDecision.RetryAt}
	}
	leaseContext, cancel := context.WithCancel(ctx)
	lease := &renewingLease{
		context: leaseContext, cancel: cancel, coordinator: a.coordinator, reference: leaseDecision.Lease,
		ttl: a.config.LeaseTTL, done: make(chan struct{}), stopped: make(chan struct{}),
	}
	go lease.renew()
	return lease, wait, nil
}

func (a *CoordinationAdapter) acquireConcurrency(ctx context.Context, leaseID string, limits []coordination.ConcurrencyLimit, deadline time.Time) (coordination.LeaseDecision, time.Duration, error) {
	startedAt := time.Now()
	for {
		decision, err := a.coordinator.AcquireLease(ctx, leaseID, a.config.LeaseTTL, limits)
		if err != nil {
			return coordination.LeaseDecision{}, time.Since(startedAt), fmt.Errorf("%w: acquire concurrency: %v", ErrCoordinationFailed, err)
		}
		if decision.Granted || deadline.IsZero() || !time.Now().Before(deadline) {
			return decision, time.Since(startedAt), nil
		}

		delay := a.config.RetryInterval
		if untilRetry := time.Until(decision.RetryAt); untilRetry > 0 && untilRetry < delay {
			delay = untilRetry
		}
		if untilDeadline := time.Until(deadline); untilDeadline < delay {
			delay = untilDeadline
		}
		if delay <= 0 {
			return decision, time.Since(startedAt), nil
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return coordination.LeaseDecision{}, time.Since(startedAt), fmt.Errorf("%w: execution capacity wait canceled: %v", ErrAdmissionCanceled, ctx.Err())
		case <-timer.C:
		}
	}
}

func admissionGateError(err error) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%w: %v", ErrAdmissionCanceled, err)
	case errors.Is(err, admission.ErrQueueTimeout):
		return fmt.Errorf("%w: %v", ErrAdmissionTimedOut, err)
	}
	var admissionError *admission.Error
	if errors.As(err, &admissionError) && admissionError.Kind == admission.ErrorQueueFull {
		return fmt.Errorf("%w: %v", ErrAdmissionQueueFull, err)
	}
	return fmt.Errorf("%w: %v", ErrCoordinationFailed, err)
}

func requestDimensions(request LeaseRequest) []coordination.Dimension {
	return []coordination.Dimension{
		coordination.GlobalDimension(),
		{Scope: coordination.ScopeResourceDomain, SubjectID: string(request.ResourceDomain)},
		{Scope: coordination.ScopeUser, SubjectID: request.UserID.String()},
		{Scope: coordination.ScopeGatewayKey, SubjectID: request.GatewayKeyID.String()},
		{Scope: coordination.ScopeModel, SubjectID: request.ModelID.String()},
		{Scope: coordination.ScopeProvider, SubjectID: request.ProviderID.String()},
		{Scope: coordination.ScopeCredential, SubjectID: request.CredentialID.String()},
	}
}

func minuteBucket(dimension coordination.Dimension, metric coordination.BucketMetric, capacity, requested int64) coordination.BucketLimit {
	return coordination.BucketLimit{
		Dimension: dimension, Metric: metric, CapacityTokens: capacity, RefillTokens: capacity,
		RefillInterval: time.Minute, RequestedTokens: requested,
	}
}

type renewingLease struct {
	context     context.Context
	cancel      context.CancelFunc
	coordinator *coordination.Coordinator
	reference   coordination.LeaseRef
	ttl         time.Duration
	done        chan struct{}
	stopped     chan struct{}
	once        sync.Once
}

func (l *renewingLease) Context() context.Context { return l.context }

func (l *renewingLease) Release(ctx context.Context) error {
	l.once.Do(func() {
		close(l.done)
		l.cancel()
	})
	select {
	case <-l.stopped:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := l.coordinator.ReleaseLease(ctx, l.reference); err != nil {
		return fmt.Errorf("release request capacity: %w", err)
	}
	return nil
}

func (l *renewingLease) renew() {
	defer close(l.stopped)
	interval := l.ttl / 3
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-l.done:
			return
		case <-l.context.Done():
			return
		case <-ticker.C:
			if _, err := l.coordinator.RenewLease(l.context, l.reference, l.ttl); err != nil {
				l.cancel()
				return
			}
		}
	}
}
