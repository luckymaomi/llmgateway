package ledger

import "time"

type Tokens int64
type EventID string
type UserID string
type SubscriptionID string
type ReservationID string
type RequestID string

type UsageSource string

const (
	UsageAuthoritative UsageSource = "authoritative"
	UsageEstimated     UsageSource = "estimated"
	UsageUnknown       UsageSource = "unknown"
)

type Usage struct {
	InputTokens  Tokens
	OutputTokens Tokens
	Source       UsageSource
}

type UsageDisposition string

const (
	UsageCharge UsageDisposition = "charge"
	UsageHold   UsageDisposition = "hold"
)

type UsageDecision struct {
	Disposition  UsageDisposition
	ChargeTokens Tokens
	Source       UsageSource
}

type EntryKind string

const (
	EntryGrant        EntryKind = "grant"
	EntryReserve      EntryKind = "reservation"
	EntrySettlement   EntryKind = "settlement"
	EntryRelease      EntryKind = "release"
	EntryCompensation EntryKind = "compensation"
)

type Entry struct {
	ID             EventID
	Kind           EntryKind
	UserID         UserID
	SubscriptionID SubscriptionID
	ReservationID  ReservationID
	RequestID      RequestID
	TokenDelta     Tokens
	ReservedTokens Tokens
	InputTokens    Tokens
	OutputTokens   Tokens
	UsageSource    UsageSource
	OccurredAt     time.Time
}

type ReservationState string

const (
	ReservationReserved    ReservationState = "reserved"
	ReservationSettled     ReservationState = "settled"
	ReservationReleased    ReservationState = "released"
	ReservationCompensated ReservationState = "compensated"
)

type ReservationSnapshot struct {
	ID              ReservationID
	RequestID       RequestID
	State           ReservationState
	ReservedTokens  Tokens
	ChargedTokens   Tokens
	UsageSource     UsageSource
	ReserveEventID  EventID
	TerminalEventID EventID
}

type Snapshot struct {
	UserID         UserID
	SubscriptionID SubscriptionID
	Balance        Tokens
	Revision       uint64
	Reservations   []ReservationSnapshot
}

type GrantCommand struct {
	EventID    EventID
	Tokens     Tokens
	OccurredAt time.Time
}

type ReserveCommand struct {
	EventID       EventID
	ReservationID ReservationID
	RequestID     RequestID
	Tokens        Tokens
	OccurredAt    time.Time
}

type SettleCommand struct {
	EventID       EventID
	ReservationID ReservationID
	Usage         Usage
	OccurredAt    time.Time
}

type ReleaseCommand struct {
	EventID       EventID
	ReservationID ReservationID
	OccurredAt    time.Time
}

type CompensateCommand struct {
	EventID       EventID
	ReservationID ReservationID
	Usage         Usage
	OccurredAt    time.Time
}

type ErrorKind string

const (
	ErrorInvalidInput       ErrorKind = "invalid_input"
	ErrorEventConflict      ErrorKind = "event_conflict"
	ErrorReservationExists  ErrorKind = "reservation_exists"
	ErrorReservationMissing ErrorKind = "reservation_missing"
	ErrorReservationState   ErrorKind = "reservation_state"
	ErrorInsufficientTokens ErrorKind = "insufficient_tokens"
	ErrorUsageUnknown       ErrorKind = "usage_unknown"
	ErrorArithmeticOverflow ErrorKind = "arithmetic_overflow"
	ErrorInvalidHistory     ErrorKind = "invalid_history"
)

type Error struct {
	Kind          ErrorKind
	Message       string
	EventID       EventID
	ReservationID ReservationID
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return e.Message
}

func newError(kind ErrorKind, message string, eventID EventID, reservationID ReservationID) *Error {
	return &Error{Kind: kind, Message: message, EventID: eventID, ReservationID: reservationID}
}
