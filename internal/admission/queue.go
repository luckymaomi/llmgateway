package admission

import (
	"sync"
	"time"
)

// Queue is a bounded, concurrency-safe scheduling core. Persistence and
// cross-instance coordination consume its decisions but remain separate owners.
type Queue struct {
	mu sync.Mutex

	config Config
	clock  Clock

	waiting       map[UserID][]Ticket
	waitingTicket map[TicketID]UserID
	userOrder     []UserID
	cursor        int
	waitingCount  int

	active       map[TicketID]Ticket
	activeByUser map[UserID]int
}

func NewQueue(config Config, clock Clock) (*Queue, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	if clock == nil {
		return nil, newError(ErrorInvalidConfiguration, "clock is required", "")
	}
	return &Queue{
		config:        config,
		clock:         clock,
		waiting:       make(map[UserID][]Ticket),
		waitingTicket: make(map[TicketID]UserID),
		active:        make(map[TicketID]Ticket),
		activeByUser:  make(map[UserID]int),
	}, nil
}

func (q *Queue) Enqueue(request Request) (Ticket, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if request.ID == "" || request.UserID == "" {
		return Ticket{}, newError(ErrorInvalidRequest, "ticket and user IDs are required", request.ID)
	}
	if _, exists := q.waitingTicket[request.ID]; exists {
		return Ticket{}, newError(ErrorDuplicateTicket, "ticket is already queued", request.ID)
	}
	if _, exists := q.active[request.ID]; exists {
		return Ticket{}, newError(ErrorDuplicateTicket, "ticket is already active", request.ID)
	}
	if q.waitingCount >= q.config.MaxQueued {
		return Ticket{}, newError(ErrorQueueFull, "admission queue is full", request.ID)
	}

	timeout := request.QueueTimeout
	if timeout == 0 {
		timeout = q.config.MaxQueueWait
	}
	if timeout < 0 || timeout > q.config.MaxQueueWait {
		return Ticket{}, newError(ErrorInvalidRequest, "queue timeout must be positive and within the configured maximum", request.ID)
	}

	now := q.clock.Now().UTC()
	ticket := Ticket{
		ID:         request.ID,
		UserID:     request.UserID,
		Priority:   request.Priority,
		EnqueuedAt: now,
		Deadline:   now.Add(timeout),
	}
	if len(q.waiting[request.UserID]) == 0 {
		q.userOrder = append(q.userOrder, request.UserID)
	}
	q.waiting[request.UserID] = append(q.waiting[request.UserID], ticket)
	q.waitingTicket[request.ID] = request.UserID
	q.waitingCount++
	return ticket, nil
}

// Dispatch expires elapsed tickets and fills every currently available slot.
// Within a priority level, users rotate; each user's own queue remains FIFO.
func (q *Queue) Dispatch() Dispatch {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := q.clock.Now().UTC()
	result := Dispatch{Expired: q.expireLocked(now)}
	for len(q.active) < q.config.MaxActive {
		ticket, ok := q.takeNextLocked()
		if !ok {
			break
		}
		q.active[ticket.ID] = ticket
		q.activeByUser[ticket.UserID]++
		result.Admitted = append(result.Admitted, Admission{Ticket: ticket, AdmittedAt: now})
	}
	return result
}

// Cancel removes a waiting ticket or releases its active permit. Repeated
// cancellation is an idempotent no-op.
func (q *Queue) Cancel(ticketID TicketID) (Cancellation, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := q.clock.Now().UTC()
	if ticket, exists := q.active[ticketID]; exists {
		delete(q.active, ticketID)
		q.decrementActiveLocked(ticket.UserID)
		return Cancellation{Ticket: ticket, CanceledAt: now, WasActive: true}, true
	}
	userID, exists := q.waitingTicket[ticketID]
	if !exists {
		return Cancellation{}, false
	}
	ticket, removed := q.removeWaitingLocked(userID, ticketID)
	if !removed {
		return Cancellation{}, false
	}
	return Cancellation{Ticket: ticket, CanceledAt: now}, true
}

// Release returns an active permit. Repeated release is an idempotent no-op.
func (q *Queue) Release(ticketID TicketID) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	ticket, exists := q.active[ticketID]
	if !exists {
		return false
	}
	delete(q.active, ticketID)
	q.decrementActiveLocked(ticket.UserID)
	return true
}

func (q *Queue) Snapshot() Snapshot {
	q.mu.Lock()
	defer q.mu.Unlock()

	snapshot := Snapshot{
		Waiting:       q.waitingCount,
		Active:        len(q.active),
		WaitingByUser: make(map[UserID]int, len(q.waiting)),
		ActiveByUser:  make(map[UserID]int, len(q.activeByUser)),
	}
	for userID, tickets := range q.waiting {
		snapshot.WaitingByUser[userID] = len(tickets)
	}
	for userID, count := range q.activeByUser {
		snapshot.ActiveByUser[userID] = count
	}
	return snapshot
}

// NextDeadline lets the workflow schedule one observable expiry wake-up
// instead of polling or using a fixed sleep.
func (q *Queue) NextDeadline() (time.Time, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var earliest time.Time
	for _, tickets := range q.waiting {
		for _, ticket := range tickets {
			if earliest.IsZero() || ticket.Deadline.Before(earliest) {
				earliest = ticket.Deadline
			}
		}
	}
	return earliest, !earliest.IsZero()
}

func (q *Queue) takeNextLocked() (Ticket, bool) {
	if q.waitingCount == 0 || len(q.userOrder) == 0 {
		return Ticket{}, false
	}

	var highest Priority
	hasEligible := false
	for _, userID := range q.userOrder {
		queue := q.waiting[userID]
		if len(queue) == 0 || q.activeByUser[userID] >= q.config.MaxActivePerUser {
			continue
		}
		if !hasEligible || queue[0].Priority > highest {
			highest = queue[0].Priority
			hasEligible = true
		}
	}
	if !hasEligible {
		return Ticket{}, false
	}

	for offset := 0; offset < len(q.userOrder); offset++ {
		index := (q.cursor + offset) % len(q.userOrder)
		userID := q.userOrder[index]
		queue := q.waiting[userID]
		if len(queue) == 0 || q.activeByUser[userID] >= q.config.MaxActivePerUser || queue[0].Priority != highest {
			continue
		}
		ticket := queue[0]
		q.waiting[userID] = queue[1:]
		delete(q.waitingTicket, ticket.ID)
		q.waitingCount--
		if len(q.waiting[userID]) == 0 {
			delete(q.waiting, userID)
			q.userOrder = append(q.userOrder[:index], q.userOrder[index+1:]...)
			if len(q.userOrder) == 0 {
				q.cursor = 0
			} else {
				q.cursor = index % len(q.userOrder)
			}
		} else {
			q.cursor = (index + 1) % len(q.userOrder)
		}
		return ticket, true
	}
	return Ticket{}, false
}

func (q *Queue) expireLocked(now time.Time) []Expiration {
	if q.waitingCount == 0 {
		return nil
	}
	anchor := UserID("")
	if len(q.userOrder) > 0 {
		anchor = q.userOrder[q.cursor%len(q.userOrder)]
	}
	order := make([]UserID, 0, len(q.userOrder))
	var expired []Expiration
	for _, userID := range q.userOrder {
		queue := q.waiting[userID]
		kept := queue[:0]
		for _, ticket := range queue {
			if !now.Before(ticket.Deadline) {
				delete(q.waitingTicket, ticket.ID)
				q.waitingCount--
				expired = append(expired, Expiration{Ticket: ticket, ExpiredAt: now})
				continue
			}
			kept = append(kept, ticket)
		}
		if len(kept) == 0 {
			delete(q.waiting, userID)
			continue
		}
		q.waiting[userID] = kept
		order = append(order, userID)
	}
	q.userOrder = order
	q.cursor = 0
	for index, userID := range q.userOrder {
		if userID == anchor {
			q.cursor = index
			break
		}
	}
	return expired
}

func (q *Queue) removeWaitingLocked(userID UserID, ticketID TicketID) (Ticket, bool) {
	queue := q.waiting[userID]
	for index, ticket := range queue {
		if ticket.ID != ticketID {
			continue
		}
		q.waiting[userID] = append(queue[:index], queue[index+1:]...)
		delete(q.waitingTicket, ticketID)
		q.waitingCount--
		if len(q.waiting[userID]) == 0 {
			delete(q.waiting, userID)
			q.removeUserLocked(userID)
		}
		return ticket, true
	}
	return Ticket{}, false
}

func (q *Queue) removeUserLocked(userID UserID) {
	for index, candidate := range q.userOrder {
		if candidate != userID {
			continue
		}
		q.userOrder = append(q.userOrder[:index], q.userOrder[index+1:]...)
		if len(q.userOrder) == 0 {
			q.cursor = 0
		} else if index < q.cursor {
			q.cursor--
		} else if q.cursor >= len(q.userOrder) {
			q.cursor = 0
		}
		return
	}
}

func (q *Queue) decrementActiveLocked(userID UserID) {
	q.activeByUser[userID]--
	if q.activeByUser[userID] == 0 {
		delete(q.activeByUser, userID)
	}
}
