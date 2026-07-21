package admission

import "time"

// Clock owns the only nondeterministic input used by the queue.
type Clock interface {
	Now() time.Time
}

type TicketID string
type UserID string

type Config struct {
	MaxQueued        int
	MaxActive        int
	MaxActivePerUser int
	MaxQueueWait     time.Duration
}

func (c Config) validate() error {
	if c.MaxQueued <= 0 {
		return newError(ErrorInvalidConfiguration, "max queued must be positive", "")
	}
	if c.MaxActive <= 0 {
		return newError(ErrorInvalidConfiguration, "max active must be positive", "")
	}
	if c.MaxActivePerUser <= 0 || c.MaxActivePerUser > c.MaxActive {
		return newError(ErrorInvalidConfiguration, "max active per user must be within the global active limit", "")
	}
	if c.MaxQueueWait <= 0 {
		return newError(ErrorInvalidConfiguration, "max queue wait must be positive", "")
	}
	return nil
}

type Request struct {
	ID           TicketID
	UserID       UserID
	QueueTimeout time.Duration
}

type Ticket struct {
	ID         TicketID
	UserID     UserID
	EnqueuedAt time.Time
	Deadline   time.Time
}

type Admission struct {
	Ticket     Ticket
	AdmittedAt time.Time
}

type Expiration struct {
	Ticket    Ticket
	ExpiredAt time.Time
}

type Cancellation struct {
	Ticket     Ticket
	CanceledAt time.Time
	WasActive  bool
}

type Dispatch struct {
	Expired  []Expiration
	Admitted []Admission
}

type Snapshot struct {
	Waiting       int
	Active        int
	WaitingByUser map[UserID]int
	ActiveByUser  map[UserID]int
}

type ErrorKind string

const (
	ErrorInvalidConfiguration ErrorKind = "invalid_configuration"
	ErrorInvalidRequest       ErrorKind = "invalid_request"
	ErrorQueueFull            ErrorKind = "queue_full"
	ErrorDuplicateTicket      ErrorKind = "duplicate_ticket"
)

type Error struct {
	Kind     ErrorKind
	Message  string
	TicketID TicketID
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return e.Message
}

func newError(kind ErrorKind, message string, ticketID TicketID) *Error {
	return &Error{Kind: kind, Message: message, TicketID: ticketID}
}
