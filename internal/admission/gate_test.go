package admission

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestGateWaitsWithoutLosingFairQueueOrder(t *testing.T) {
	gate, err := NewGate(Config{MaxQueued: 4, MaxActive: 1, MaxActivePerUser: 1, MaxQueueWait: time.Minute}, wallClock{})
	if err != nil {
		t.Fatalf("NewGate() error = %v", err)
	}
	first, _, err := gate.Acquire(context.Background(), Request{ID: "first", UserID: "alice"})
	if err != nil {
		t.Fatalf("Acquire(first) error = %v", err)
	}
	secondResult := make(chan *Permit, 1)
	go func() {
		permit, _, acquireErr := gate.Acquire(context.Background(), Request{ID: "second", UserID: "bob"})
		if acquireErr == nil {
			secondResult <- permit
		}
	}()
	select {
	case <-secondResult:
		t.Fatal("second request bypassed the active permit")
	default:
	}
	first.Release()
	select {
	case second := <-secondResult:
		second.Release()
	case <-time.After(time.Second):
		t.Fatal("second request was not admitted after release")
	}
}

func TestGateCancellationRemovesWaitingTicket(t *testing.T) {
	gate, err := NewGate(Config{MaxQueued: 4, MaxActive: 1, MaxActivePerUser: 1, MaxQueueWait: time.Minute}, wallClock{})
	if err != nil {
		t.Fatalf("NewGate() error = %v", err)
	}
	first, _, err := gate.Acquire(context.Background(), Request{ID: "first", UserID: "alice"})
	if err != nil {
		t.Fatalf("Acquire(first) error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	canceled := make(chan error, 1)
	go func() {
		_, _, acquireErr := gate.Acquire(ctx, Request{ID: "canceled", UserID: "bob"})
		canceled <- acquireErr
	}()
	cancel()
	if err := <-canceled; !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire(canceled) error = %v", err)
	}
	first.Release()
	third, _, err := gate.Acquire(context.Background(), Request{ID: "third", UserID: "carol"})
	if err != nil {
		t.Fatalf("Acquire(third) error = %v", err)
	}
	third.Release()
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }
