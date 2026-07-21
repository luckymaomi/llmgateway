package admission

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrQueueTimeout = errors.New("admission queue wait timed out")

type Gate struct {
	mu      sync.Mutex
	queue   *Queue
	waiters map[TicketID]chan gateResult
}

type gateResult struct {
	admission Admission
	err       error
}

type Permit struct {
	once sync.Once
	gate *Gate
	id   TicketID
}

func NewGate(config Config, clock Clock) (*Gate, error) {
	queue, err := NewQueue(config, clock)
	if err != nil {
		return nil, err
	}
	return &Gate{queue: queue, waiters: make(map[TicketID]chan gateResult)}, nil
}

func (g *Gate) Acquire(ctx context.Context, request Request) (*Permit, time.Duration, error) {
	waiter := make(chan gateResult, 1)
	g.mu.Lock()
	ticket, err := g.queue.Enqueue(request)
	if err != nil {
		g.mu.Unlock()
		return nil, 0, err
	}
	g.waiters[ticket.ID] = waiter
	g.dispatchLocked()
	g.mu.Unlock()

	timer := time.NewTimer(time.Until(ticket.Deadline))
	defer timer.Stop()
	select {
	case result := <-waiter:
		if result.err != nil {
			return nil, 0, result.err
		}
		return &Permit{gate: g, id: ticket.ID}, result.admission.AdmittedAt.Sub(ticket.EnqueuedAt), nil
	case <-ctx.Done():
		g.cancel(ticket.ID)
		return nil, 0, ctx.Err()
	case <-timer.C:
		g.cancel(ticket.ID)
		return nil, 0, ErrQueueTimeout
	}
}

func (p *Permit) Release() {
	if p == nil || p.gate == nil {
		return
	}
	p.once.Do(func() {
		p.gate.release(p.id)
	})
}

func (g *Gate) cancel(ticketID TicketID) {
	g.mu.Lock()
	delete(g.waiters, ticketID)
	g.queue.Cancel(ticketID)
	g.dispatchLocked()
	g.mu.Unlock()
}

func (g *Gate) dispatch() {
	g.mu.Lock()
	g.dispatchLocked()
	g.mu.Unlock()
}

func (g *Gate) release(ticketID TicketID) {
	g.mu.Lock()
	g.queue.Release(ticketID)
	g.dispatchLocked()
	g.mu.Unlock()
}

func (g *Gate) dispatchLocked() {
	dispatch := g.queue.Dispatch()
	for _, expiration := range dispatch.Expired {
		g.deliverLocked(expiration.Ticket.ID, gateResult{err: ErrQueueTimeout})
	}
	for _, admitted := range dispatch.Admitted {
		if !g.deliverLocked(admitted.Ticket.ID, gateResult{admission: admitted}) {
			g.queue.Release(admitted.Ticket.ID)
		}
	}
}

func (g *Gate) deliver(ticketID TicketID, result gateResult) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.deliverLocked(ticketID, result)
}

func (g *Gate) deliverLocked(ticketID TicketID, result gateResult) bool {
	waiter := g.waiters[ticketID]
	delete(g.waiters, ticketID)
	if waiter == nil {
		return false
	}
	waiter <- result
	return true
}
