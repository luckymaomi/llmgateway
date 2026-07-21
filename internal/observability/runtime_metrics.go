package observability

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/execution"
	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/requestflow"
	"github.com/prometheus/client_golang/prometheus"
)

type RuntimeMetrics struct {
	logger             *slog.Logger
	admissionRequests  *prometheus.CounterVec
	admissionWait      *prometheus.HistogramVec
	coordinationLeases *prometheus.CounterVec
	coordinationActive *prometheus.GaugeVec
	coordinationWait   *prometheus.HistogramVec
	providerAttempts   *prometheus.CounterVec
	quotaOperations    *prometheus.CounterVec
	requestRecovery    *prometheus.CounterVec
	background         *prometheus.CounterVec
}

func NewRuntimeMetrics(registry prometheus.Registerer, loggers ...*slog.Logger) *RuntimeMetrics {
	metrics := &RuntimeMetrics{
		admissionRequests:  prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "llmgateway", Subsystem: "admission", Name: "requests_total", Help: "Local and shared admission outcomes."}, []string{"outcome"}),
		admissionWait:      prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: "llmgateway", Subsystem: "admission", Name: "wait_seconds", Help: "Time spent waiting for admission.", Buckets: []float64{0.001, 0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10, 30}}, []string{"outcome"}),
		coordinationLeases: prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "llmgateway", Subsystem: "coordination", Name: "leases_total", Help: "Shared request lease acquisition outcomes."}, []string{"outcome", "resource_domain"}),
		coordinationActive: prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: "llmgateway", Subsystem: "coordination", Name: "active_leases", Help: "Currently held shared request leases."}, []string{"resource_domain"}),
		coordinationWait:   prometheus.NewHistogramVec(prometheus.HistogramOpts{Namespace: "llmgateway", Subsystem: "coordination", Name: "wait_seconds", Help: "Time spent acquiring shared request capacity.", Buckets: []float64{0.001, 0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10, 30}}, []string{"outcome"}),
		providerAttempts:   prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "llmgateway", Subsystem: "provider", Name: "attempts_total", Help: "Terminal Provider attempt outcomes."}, []string{"provider_kind", "outcome", "error_kind"}),
		quotaOperations:    prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "llmgateway", Subsystem: "quota", Name: "operations_total", Help: "Quota reservation and settlement operation outcomes."}, []string{"operation", "outcome", "resource_domain"}),
		requestRecovery:    prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "llmgateway", Subsystem: "request_recovery", Name: "results_total", Help: "Recovered stale request outcomes."}, []string{"outcome"}),
		background:         prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: "llmgateway", Subsystem: "background", Name: "responses_total", Help: "Background Responses lifecycle outcomes."}, []string{"outcome"}),
	}
	if len(loggers) > 0 {
		metrics.logger = loggers[0]
	}
	registry.MustRegister(metrics.admissionRequests, metrics.admissionWait, metrics.coordinationLeases, metrics.coordinationActive, metrics.coordinationWait, metrics.providerAttempts, metrics.quotaOperations, metrics.requestRecovery, metrics.background)
	metrics.initialize()
	return metrics
}

func (m *RuntimeMetrics) initialize() {
	for _, outcome := range []string{"admitted", "queue_full", "timeout", "canceled", "unavailable", "capacity_exhausted", "failed"} {
		m.admissionRequests.WithLabelValues(outcome)
		m.admissionWait.WithLabelValues(outcome)
	}
	for _, outcome := range []string{"acquired", "capacity_exhausted", "unavailable", "failed"} {
		m.coordinationWait.WithLabelValues(outcome)
	}
	for _, domain := range []string{"free", "professional", "unknown"} {
		m.coordinationActive.WithLabelValues(domain)
		for _, outcome := range []string{"acquired", "capacity_exhausted", "unavailable", "failed"} {
			m.coordinationLeases.WithLabelValues(outcome, domain)
		}
	}
	for _, kind := range []providers.Kind{providers.KindAgnes, providers.KindGemini, providers.KindOpenAICompatible, providers.KindZhipu} {
		m.providerAttempts.WithLabelValues(string(kind), "succeeded", "none")
	}
	for _, operation := range []string{"reserve", "settle", "release", "release_accepted", "compensate"} {
		m.quotaOperations.WithLabelValues(operation, "succeeded", "unknown")
		m.quotaOperations.WithLabelValues(operation, "failed", "unknown")
	}
	for _, outcome := range []string{"settled", "released", "uncertain", "failed"} {
		m.requestRecovery.WithLabelValues(outcome)
	}
	for _, outcome := range []string{"queued", "claimed", "completed", "failed", "canceled", "uncertain", "recovered_completed", "recovered_failed", "recovered_canceled", "recovered_uncertain"} {
		m.background.WithLabelValues(outcome)
	}
}

func (m *RuntimeMetrics) ProviderAttempt(providerKind providers.Kind, outcome, errorKind string) {
	m.providerAttempts.WithLabelValues(string(providerKind), outcome, errorKind).Inc()
	if outcome != "succeeded" {
		m.log("provider.attempt_terminal", "provider_kind", providerKind, "outcome", outcome, "error_kind", errorKind)
	}
}

func (m *RuntimeMetrics) BackgroundResponse(outcome string) {
	m.background.WithLabelValues(outcome).Inc()
	if outcome == "failed" || outcome == "uncertain" || outcome == "recovered_failed" || outcome == "recovered_uncertain" {
		m.log("background.response_terminal", "outcome", outcome)
	}
}

func (m *RuntimeMetrics) RequestRecovery(result requestflow.RecoveryResult) {
	for outcome, count := range map[string]int64{"settled": result.Settled, "released": result.Released, "uncertain": result.Uncertain} {
		if count > 0 {
			m.requestRecovery.WithLabelValues(outcome).Add(float64(count))
			if outcome == "uncertain" {
				m.log("request.recovery_terminal", "outcome", outcome, "count", count)
			}
		}
	}
}

func (m *RuntimeMetrics) RequestRecoveryFailed() {
	m.requestRecovery.WithLabelValues("failed").Inc()
	m.log("request.recovery_terminal", "outcome", "failed", "count", 1)
}

func (m *RuntimeMetrics) log(event string, attributes ...any) {
	if m.logger == nil {
		return
	}
	m.logger.Warn("runtime domain event", append([]any{"event", event}, attributes...)...)
}

func (m *RuntimeMetrics) ObserveAdmitter(next requestflow.Admitter) requestflow.Admitter {
	return observedAdmitter{next: next, metrics: m}
}

type observedAdmitter struct {
	next    requestflow.Admitter
	metrics *RuntimeMetrics
}

func (a observedAdmitter) Acquire(ctx context.Context, request requestflow.AdmissionRequest) (requestflow.AdmissionPermit, time.Duration, error) {
	permit, wait, err := a.next.Acquire(ctx, request)
	outcome := "admitted"
	if err != nil {
		outcome = admissionOutcome(err)
	}
	a.metrics.admissionRequests.WithLabelValues(outcome).Inc()
	a.metrics.admissionWait.WithLabelValues(outcome).Observe(wait.Seconds())
	if err != nil {
		a.metrics.log("admission.rejected", "outcome", outcome)
	}
	return permit, wait, err
}

func admissionOutcome(err error) string {
	switch {
	case errors.Is(err, requestflow.ErrAdmissionQueueFull):
		return "queue_full"
	case errors.Is(err, requestflow.ErrAdmissionTimedOut):
		return "timeout"
	case errors.Is(err, requestflow.ErrAdmissionCanceled), errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, requestflow.ErrCoordinationFailed):
		return "unavailable"
	default:
		var capacity *requestflow.CapacityError
		if errors.As(err, &capacity) {
			return "capacity_exhausted"
		}
		return "failed"
	}
}

func (m *RuntimeMetrics) ObserveCoordinator(next requestflow.Coordinator) requestflow.Coordinator {
	return observedCoordinator{next: next, metrics: m}
}

type observedCoordinator struct {
	next    requestflow.Coordinator
	metrics *RuntimeMetrics
}

func (c observedCoordinator) Acquire(ctx context.Context, request requestflow.LeaseRequest) (requestflow.Lease, time.Duration, error) {
	lease, wait, err := c.next.Acquire(ctx, request)
	outcome := "acquired"
	if err != nil {
		outcome = admissionOutcome(err)
	}
	domain := string(request.ResourceDomain)
	if domain == "" {
		domain = "unknown"
	}
	c.metrics.coordinationLeases.WithLabelValues(outcome, domain).Inc()
	c.metrics.coordinationWait.WithLabelValues(outcome).Observe(wait.Seconds())
	if err != nil {
		c.metrics.log("coordination.acquire_failed", "outcome", outcome, "resource_domain", domain)
		return lease, wait, err
	}
	c.metrics.coordinationActive.WithLabelValues(domain).Inc()
	return &observedLease{next: lease, release: func() { c.metrics.coordinationActive.WithLabelValues(domain).Dec() }}, wait, nil
}

type observedLease struct {
	next    requestflow.Lease
	once    sync.Once
	release func()
}

func (l *observedLease) Context() context.Context { return l.next.Context() }

func (l *observedLease) Release(ctx context.Context) error {
	err := l.next.Release(ctx)
	l.once.Do(l.release)
	return err
}

func (m *RuntimeMetrics) ObserveAccounting(next requestflow.Accounting) requestflow.Accounting {
	return observedAccounting{next: next, metrics: m}
}

type observedAccounting struct {
	next    requestflow.Accounting
	metrics *RuntimeMetrics
}

func (a observedAccounting) AcceptRequest(ctx context.Context, command requestflow.AcceptCommand) (requestflow.Accepted, error) {
	result, err := a.next.AcceptRequest(ctx, command)
	a.record("reserve", string(command.ResourceDomain), err)
	return result, err
}

func (a observedAccounting) Settle(ctx context.Context, claim execution.Claim, usage requestflow.Usage) error {
	err := a.next.Settle(ctx, claim, usage)
	a.record("settle", "unknown", err)
	return err
}

func (a observedAccounting) Release(ctx context.Context, claim execution.Claim, kind, detail string) error {
	err := a.next.Release(ctx, claim, kind, detail)
	a.record("release", "unknown", err)
	return err
}

func (a observedAccounting) ReleaseAccepted(ctx context.Context, requestID uuid.UUID, kind, detail string) error {
	err := a.next.ReleaseAccepted(ctx, requestID, kind, detail)
	a.record("release_accepted", "unknown", err)
	return err
}

func (a observedAccounting) Compensate(ctx context.Context, claim execution.Claim, usage requestflow.Usage, reason string) error {
	err := a.next.Compensate(ctx, claim, usage, reason)
	a.record("compensate", "unknown", err)
	return err
}

func (a observedAccounting) record(operation, domain string, err error) {
	outcome := "succeeded"
	if err != nil {
		outcome = "failed"
	}
	a.metrics.quotaOperations.WithLabelValues(operation, outcome, domain).Inc()
	if err != nil {
		a.metrics.log("quota.operation_failed", "operation", operation, "resource_domain", domain)
	}
}
