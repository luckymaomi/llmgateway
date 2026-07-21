package main

import (
	"bytes"
	"context"
	"time"
)

func (f *fixture) beginRequest(payload []byte) func() {
	f.requests.Add(1)
	active := f.active.Add(1)
	for {
		peak := f.peakActive.Load()
		if active <= peak || f.peakActive.CompareAndSwap(peak, active) {
			break
		}
	}
	switch {
	case bytes.Contains(payload, []byte("capacity extended stream")):
		f.extendedStreams.Add(1)
		f.streams.Add(1)
	case bytes.Contains(payload, []byte("capacity long stream")):
		f.longStreams.Add(1)
		f.streams.Add(1)
	case bytes.Contains(payload, []byte("capacity short stream")):
		f.streams.Add(1)
	case bytes.Contains(payload, []byte("capacity background")):
		f.background.Add(1)
	case bytes.Contains(payload, []byte("capacity tool reasoning")):
		f.toolReason.Add(1)
	case bytes.Contains(payload, []byte("capacity short")):
		f.short.Add(1)
	}
	return func() { f.active.Add(-1) }
}

func (f *fixture) waitForScenario(ctx context.Context, payload []byte) bool {
	delay := time.Duration(0)
	switch {
	case bytes.Contains(payload, []byte("capacity short")):
		delay = 20 * time.Millisecond
	case bytes.Contains(payload, []byte("capacity background")):
		delay = 35 * time.Millisecond
	case bytes.Contains(payload, []byte("capacity tool reasoning")):
		delay = 30 * time.Millisecond
	}
	if delay == 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
