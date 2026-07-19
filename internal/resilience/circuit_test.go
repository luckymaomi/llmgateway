package resilience

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestCircuit(t *testing.T, clock *manualClock, config CircuitConfig) *Circuit {
	t.Helper()
	circuit, err := NewCircuit(config, clock)
	if err != nil {
		t.Fatalf("NewCircuit() error = %v", err)
	}
	return circuit
}

func completePermit(t *testing.T, result AcquireResult, outcome PermitResult) {
	t.Helper()
	if !result.Allowed || result.Permit == nil {
		t.Fatalf("Acquire() = %#v", result)
	}
	if !result.Permit.Complete(outcome) {
		t.Fatalf("permit outcome %q was not applied", outcome)
	}
}

func TestCircuitTransitionsClosedOpenHalfOpenAndClosed(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)}
	circuit := newTestCircuit(t, clock, CircuitConfig{
		FailureThreshold: 2, SuccessThreshold: 2, OpenDuration: time.Minute, HalfOpenMaxInFlight: 2,
	})

	completePermit(t, circuit.Acquire(), PermitFailed)
	if snapshot := circuit.Snapshot(); snapshot.State != CircuitClosed || snapshot.ConsecutiveFailures != 1 {
		t.Fatalf("snapshot after first failure = %#v", snapshot)
	}
	completePermit(t, circuit.Acquire(), PermitFailed)
	opened := circuit.Snapshot()
	if opened.State != CircuitOpen || !opened.RetryAt.Equal(clock.Now().Add(time.Minute)) {
		t.Fatalf("opened snapshot = %#v", opened)
	}
	if result := circuit.Acquire(); result.Allowed || result.State != CircuitOpen || !result.RetryAt.Equal(opened.RetryAt) {
		t.Fatalf("open Acquire() = %#v", result)
	}

	clock.Advance(time.Minute)
	firstProbe := circuit.Acquire()
	secondProbe := circuit.Acquire()
	if !firstProbe.Allowed || !secondProbe.Allowed || firstProbe.State != CircuitHalfOpen {
		t.Fatalf("probe results = %#v, %#v", firstProbe, secondProbe)
	}
	if result := circuit.Acquire(); result.Allowed || result.State != CircuitHalfOpen {
		t.Fatalf("bounded probe Acquire() = %#v", result)
	}
	completePermit(t, firstProbe, PermitSucceeded)
	if snapshot := circuit.Snapshot(); snapshot.State != CircuitHalfOpen || snapshot.HalfOpenSuccesses != 1 {
		t.Fatalf("snapshot after first probe = %#v", snapshot)
	}
	completePermit(t, secondProbe, PermitSucceeded)
	if snapshot := circuit.Snapshot(); snapshot.State != CircuitClosed {
		t.Fatalf("closed snapshot = %#v", snapshot)
	}
}

func TestCircuitFailureDuringProbeReopensForAFullWindow(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)}
	circuit := newTestCircuit(t, clock, CircuitConfig{
		FailureThreshold: 1, SuccessThreshold: 1, OpenDuration: 30 * time.Second, HalfOpenMaxInFlight: 1,
	})
	completePermit(t, circuit.Acquire(), PermitFailed)
	clock.Advance(30 * time.Second)
	probe := circuit.Acquire()
	if probe.Permit.Complete(PermitResult("invalid")) {
		t.Fatal("invalid probe outcome was applied")
	}
	completePermit(t, probe, PermitFailed)

	snapshot := circuit.Snapshot()
	if snapshot.State != CircuitOpen || !snapshot.RetryAt.Equal(clock.Now().Add(30*time.Second)) {
		t.Fatalf("reopened snapshot = %#v", snapshot)
	}
}

func TestCircuitIgnoresLatePermitFromAnEarlierGeneration(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)}
	circuit := newTestCircuit(t, clock, CircuitConfig{
		FailureThreshold: 1, SuccessThreshold: 1, OpenDuration: time.Minute, HalfOpenMaxInFlight: 1,
	})
	late := circuit.Acquire()
	completePermit(t, circuit.Acquire(), PermitFailed)
	if late.Permit.Complete(PermitSucceeded) {
		t.Fatal("late permit was applied to a newer circuit generation")
	}
	if snapshot := circuit.Snapshot(); snapshot.State != CircuitOpen {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestCircuitBoundsConcurrentHalfOpenProbes(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)}
	circuit := newTestCircuit(t, clock, CircuitConfig{
		FailureThreshold: 1, SuccessThreshold: 2, OpenDuration: time.Minute, HalfOpenMaxInFlight: 2,
	})
	completePermit(t, circuit.Acquire(), PermitFailed)
	clock.Advance(time.Minute)

	const workers = 32
	start := make(chan struct{})
	release := make(chan struct{})
	var acquired sync.WaitGroup
	var finished sync.WaitGroup
	var allowed atomic.Int32
	acquired.Add(workers)
	finished.Add(workers)
	for index := 0; index < workers; index++ {
		go func() {
			defer finished.Done()
			<-start
			result := circuit.Acquire()
			if result.Allowed {
				allowed.Add(1)
			}
			acquired.Done()
			<-release
			if result.Allowed {
				result.Permit.Complete(PermitReleased)
			}
		}()
	}
	close(start)
	acquired.Wait()
	if got := allowed.Load(); got != 2 {
		t.Fatalf("allowed probes = %d, want 2", got)
	}
	close(release)
	finished.Wait()
	if snapshot := circuit.Snapshot(); snapshot.HalfOpenInFlight != 0 {
		t.Fatalf("snapshot after probe release = %#v", snapshot)
	}
}
