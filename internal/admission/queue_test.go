package admission

import (
	"sync"
	"testing"
	"time"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	c.mu.Unlock()
}

func newTestQueue(t *testing.T, config Config) (*Queue, *testClock) {
	t.Helper()
	clock := &testClock{now: time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)}
	queue, err := NewQueue(config, clock)
	if err != nil {
		t.Fatalf("NewQueue() error = %v", err)
	}
	return queue, clock
}

func enqueue(t *testing.T, queue *Queue, id, user string, priority Priority) Ticket {
	t.Helper()
	ticket, err := queue.Enqueue(Request{ID: TicketID(id), UserID: UserID(user), Priority: priority})
	if err != nil {
		t.Fatalf("Enqueue(%s) error = %v", id, err)
	}
	return ticket
}

func admittedIDs(dispatch Dispatch) []TicketID {
	ids := make([]TicketID, 0, len(dispatch.Admitted))
	for _, admission := range dispatch.Admitted {
		ids = append(ids, admission.Ticket.ID)
	}
	return ids
}

func TestQueueRotatesUsersWhileKeepingEachUserFIFO(t *testing.T) {
	queue, _ := newTestQueue(t, Config{MaxQueued: 8, MaxActive: 4, MaxActivePerUser: 2, MaxQueueWait: time.Minute})
	enqueue(t, queue, "a1", "alice", 10)
	enqueue(t, queue, "a2", "alice", 10)
	enqueue(t, queue, "b1", "bob", 10)
	enqueue(t, queue, "b2", "bob", 10)

	got := admittedIDs(queue.Dispatch())
	want := []TicketID{"a1", "b1", "a2", "b2"}
	if len(got) != len(want) {
		t.Fatalf("admitted IDs = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("admitted IDs = %v, want %v", got, want)
		}
	}
}

func TestQueueHonorsHeadPriorityWithoutReorderingAUser(t *testing.T) {
	queue, _ := newTestQueue(t, Config{MaxQueued: 8, MaxActive: 3, MaxActivePerUser: 2, MaxQueueWait: time.Minute})
	enqueue(t, queue, "a1", "alice", 1)
	enqueue(t, queue, "a2", "alice", 100)
	enqueue(t, queue, "b1", "bob", 50)

	got := admittedIDs(queue.Dispatch())
	want := []TicketID{"b1", "a1", "a2"}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("admitted IDs = %v, want %v", got, want)
		}
	}
}

func TestQueueBoundsOneUsersActiveShare(t *testing.T) {
	queue, _ := newTestQueue(t, Config{MaxQueued: 8, MaxActive: 2, MaxActivePerUser: 1, MaxQueueWait: time.Minute})
	enqueue(t, queue, "a1", "alice", 1)
	enqueue(t, queue, "a2", "alice", 1)
	enqueue(t, queue, "b1", "bob", 1)

	first := admittedIDs(queue.Dispatch())
	want := []TicketID{"a1", "b1"}
	for index := range want {
		if first[index] != want[index] {
			t.Fatalf("first dispatch = %v, want %v", first, want)
		}
	}
	if !queue.Release("a1") {
		t.Fatal("Release(a1) did not release the active permit")
	}
	second := admittedIDs(queue.Dispatch())
	if len(second) != 1 || second[0] != "a2" {
		t.Fatalf("second dispatch = %v, want [a2]", second)
	}
}

func TestQueueReportsCancellationAndTimeout(t *testing.T) {
	queue, clock := newTestQueue(t, Config{MaxQueued: 3, MaxActive: 1, MaxActivePerUser: 1, MaxQueueWait: time.Minute})
	enqueue(t, queue, "active", "alice", 1)
	enqueue(t, queue, "cancel", "bob", 1)
	_, err := queue.Enqueue(Request{ID: "expire", UserID: "carol", Priority: 1, QueueTimeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("Enqueue(expire) error = %v", err)
	}
	queue.Dispatch()
	deadline, found := queue.NextDeadline()
	if !found || !deadline.Equal(clock.Now().Add(10*time.Second)) {
		t.Fatalf("NextDeadline() = %v, %v", deadline, found)
	}

	cancellation, canceled := queue.Cancel("cancel")
	if !canceled || cancellation.Ticket.ID != "cancel" || cancellation.WasActive {
		t.Fatalf("Cancel(cancel) = %#v, %v", cancellation, canceled)
	}
	clock.Advance(10 * time.Second)
	dispatch := queue.Dispatch()
	if len(dispatch.Expired) != 1 || dispatch.Expired[0].Ticket.ID != "expire" {
		t.Fatalf("expired = %#v, want expire", dispatch.Expired)
	}
	snapshot := queue.Snapshot()
	if snapshot.Waiting != 0 || snapshot.Active != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if _, found := queue.NextDeadline(); found {
		t.Fatal("NextDeadline() reported a deadline after the waiting queue drained")
	}
}

func TestQueueCancellationReleasesAnActivePermit(t *testing.T) {
	queue, _ := newTestQueue(t, Config{MaxQueued: 2, MaxActive: 1, MaxActivePerUser: 1, MaxQueueWait: time.Minute})
	enqueue(t, queue, "first", "alice", 1)
	enqueue(t, queue, "second", "bob", 1)
	queue.Dispatch()

	cancellation, canceled := queue.Cancel("first")
	if !canceled || !cancellation.WasActive {
		t.Fatalf("Cancel(first) = %#v, %v", cancellation, canceled)
	}
	got := admittedIDs(queue.Dispatch())
	if len(got) != 1 || got[0] != "second" {
		t.Fatalf("dispatch after cancellation = %v, want [second]", got)
	}
}
