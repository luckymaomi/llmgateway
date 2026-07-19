package admission

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestQueueAcceptsConcurrentUsersAndAdmitsEveryTicketOnce(t *testing.T) {
	const total = 96
	queue, _ := newTestQueue(t, Config{
		MaxQueued: total, MaxActive: total, MaxActivePerUser: 1, MaxQueueWait: time.Minute,
	})
	start := make(chan struct{})
	errors := make(chan error, total)
	var workers sync.WaitGroup
	for index := 0; index < total; index++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			<-start
			_, err := queue.Enqueue(Request{
				ID: TicketID(fmt.Sprintf("ticket-%03d", index)), UserID: UserID(fmt.Sprintf("user-%03d", index)),
			})
			errors <- err
		}(index)
	}
	close(start)
	workers.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent Enqueue() error = %v", err)
		}
	}

	dispatch := queue.Dispatch()
	if len(dispatch.Admitted) != total {
		t.Fatalf("admitted = %d, want %d", len(dispatch.Admitted), total)
	}
	seen := make(map[TicketID]struct{}, total)
	for _, admission := range dispatch.Admitted {
		if _, exists := seen[admission.Ticket.ID]; exists {
			t.Fatalf("ticket %q was admitted twice", admission.Ticket.ID)
		}
		seen[admission.Ticket.ID] = struct{}{}
	}
}
