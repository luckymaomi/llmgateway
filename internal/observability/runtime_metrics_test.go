package observability

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/requestflow"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRuntimeMetricsExposeBoundedDomainOutcomes(t *testing.T) {
	registry := prometheus.NewRegistry()
	var logs bytes.Buffer
	metrics := NewRuntimeMetrics(registry, slog.New(slog.NewJSONHandler(&logs, nil)))
	metrics.ProviderAttempt(providers.KindGemini, "uncertain", "uncertain")
	metrics.BackgroundResponse("completed")
	metrics.RequestRecovery(requestflow.RecoveryResult{Settled: 2, Released: 1, Uncertain: 1})

	if got := testutil.ToFloat64(metrics.providerAttempts.WithLabelValues("gemini", "uncertain", "uncertain")); got != 1 {
		t.Fatalf("Provider attempts = %v", got)
	}
	if got := testutil.ToFloat64(metrics.requestRecovery.WithLabelValues("settled")); got != 2 {
		t.Fatalf("settled recoveries = %v", got)
	}
	if count, err := registry.Gather(); err != nil || len(count) < 8 {
		t.Fatalf("Gather() families = %d, error = %v", len(count), err)
	}
	for _, event := range []string{"provider.attempt_terminal", "request.recovery_terminal"} {
		if !strings.Contains(logs.String(), `"event":"`+event+`"`) {
			t.Fatalf("runtime logs do not contain stable event %q: %s", event, logs.String())
		}
	}
}

func TestObservedBoundariesPreserveBehaviorAndReleaseGaugeOnce(t *testing.T) {
	metrics := NewRuntimeMetrics(prometheus.NewRegistry())
	admitter := metrics.ObserveAdmitter(admitterStub{err: requestflow.ErrAdmissionQueueFull, wait: 25 * time.Millisecond})
	if _, _, err := admitter.Acquire(context.Background(), requestflow.AdmissionRequest{RequestID: uuid.New(), UserID: uuid.New()}); !errors.Is(err, requestflow.ErrAdmissionQueueFull) {
		t.Fatalf("Acquire() error = %v", err)
	}
	if got := testutil.ToFloat64(metrics.admissionRequests.WithLabelValues("queue_full")); got != 1 {
		t.Fatalf("queue-full admissions = %v", got)
	}

	coordinator := metrics.ObserveCoordinator(coordinatorStub{lease: leaseStub{ctx: context.Background()}})
	lease, _, err := coordinator.Acquire(context.Background(), requestflow.LeaseRequest{ResourceDomain: "free"})
	if err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(metrics.coordinationActive.WithLabelValues("free")); got != 1 {
		t.Fatalf("active leases before release = %v", got)
	}
	if err := lease.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ToFloat64(metrics.coordinationActive.WithLabelValues("free")); got != 0 {
		t.Fatalf("active leases after repeated release = %v", got)
	}
}

type admitterStub struct {
	wait time.Duration
	err  error
}

func (s admitterStub) Acquire(context.Context, requestflow.AdmissionRequest) (requestflow.AdmissionPermit, time.Duration, error) {
	return nil, s.wait, s.err
}

type coordinatorStub struct {
	lease requestflow.Lease
}

func (s coordinatorStub) Acquire(context.Context, requestflow.LeaseRequest) (requestflow.Lease, time.Duration, error) {
	return s.lease, 10 * time.Millisecond, nil
}

type leaseStub struct {
	ctx context.Context
}

func (l leaseStub) Context() context.Context    { return l.ctx }
func (leaseStub) Release(context.Context) error { return nil }
