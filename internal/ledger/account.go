package ledger

import (
	"sort"
	"sync"
	"time"
)

type reservation struct {
	ID              ReservationID
	RequestID       RequestID
	State           ReservationState
	ReservedTokens  Tokens
	ChargedTokens   Tokens
	UsageSource     UsageSource
	ReserveEventID  EventID
	TerminalEventID EventID
}

// Account is the ledger aggregate for one entitlement. A persistent repository
// must load and append it under a transaction or equivalent revision check.
type Account struct {
	mu sync.Mutex

	userID        UserID
	entitlementID EntitlementID
	balance       Tokens
	entries       []Entry
	events        map[EventID]Entry
	reservations  map[ReservationID]*reservation
}

func NewAccount(userID UserID, entitlementID EntitlementID) (*Account, error) {
	if userID == "" || entitlementID == "" {
		return nil, newError(ErrorInvalidInput, "user and entitlement IDs are required", "", "")
	}
	return &Account{
		userID: userID, entitlementID: entitlementID,
		events: make(map[EventID]Entry), reservations: make(map[ReservationID]*reservation),
	}, nil
}

func (a *Account) Grant(command GrantCommand) (Entry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := validateEvent(command.EventID, command.OccurredAt); err != nil {
		return Entry{}, err
	}
	if command.Tokens <= 0 {
		return Entry{}, newError(ErrorInvalidInput, "grant tokens must be positive", command.EventID, "")
	}
	entry := Entry{
		ID: command.EventID, Kind: EntryGrant, UserID: a.userID, EntitlementID: a.entitlementID,
		TokenDelta: command.Tokens, UsageSource: UsageUnknown, OccurredAt: command.OccurredAt.UTC(),
	}
	if existing, handled, err := a.idempotentEntryLocked(entry); handled {
		return existing, err
	}
	if err := a.appendLocked(entry); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func (a *Account) Reserve(command ReserveCommand) (Entry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := validateEvent(command.EventID, command.OccurredAt); err != nil {
		return Entry{}, err
	}
	if command.ReservationID == "" || command.RequestID == "" || command.Tokens <= 0 {
		return Entry{}, newError(ErrorInvalidInput, "reservation, request, and positive token count are required", command.EventID, command.ReservationID)
	}
	entry := Entry{
		ID: command.EventID, Kind: EntryReserve, UserID: a.userID, EntitlementID: a.entitlementID,
		ReservationID: command.ReservationID, RequestID: command.RequestID,
		TokenDelta: -command.Tokens, ReservedTokens: command.Tokens,
		UsageSource: UsageEstimated, OccurredAt: command.OccurredAt.UTC(),
	}
	if existing, handled, err := a.idempotentEntryLocked(entry); handled {
		return existing, err
	}
	if _, exists := a.reservations[command.ReservationID]; exists {
		return Entry{}, newError(ErrorReservationExists, "reservation already exists", command.EventID, command.ReservationID)
	}
	if a.balance < command.Tokens {
		return Entry{}, newError(ErrorInsufficientTokens, "available token balance is below the reservation", command.EventID, command.ReservationID)
	}
	if err := a.appendLocked(entry); err != nil {
		return Entry{}, err
	}
	a.reservations[command.ReservationID] = &reservation{
		ID: command.ReservationID, RequestID: command.RequestID, State: ReservationReserved,
		ReservedTokens: command.Tokens, UsageSource: UsageUnknown, ReserveEventID: command.EventID,
	}
	return entry, nil
}

func (a *Account) Settle(command SettleCommand) (Entry, error) {
	return a.resolve(command.EventID, command.ReservationID, command.Usage, command.OccurredAt, EntrySettlement, ReservationSettled)
}

func (a *Account) Release(command ReleaseCommand) (Entry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := validateEvent(command.EventID, command.OccurredAt); err != nil {
		return Entry{}, err
	}
	current, exists := a.reservations[command.ReservationID]
	if !exists {
		return Entry{}, newError(ErrorReservationMissing, "reservation was not found", command.EventID, command.ReservationID)
	}
	entry := terminalEntry(a, command.EventID, current, EntryRelease, current.ReservedTokens, Usage{Source: UsageUnknown}, command.OccurredAt)
	if existing, handled, err := a.idempotentEntryLocked(entry); handled {
		return existing, err
	}
	if current.State != ReservationReserved {
		return Entry{}, newError(ErrorReservationState, "reservation is already terminal", command.EventID, command.ReservationID)
	}
	if err := a.appendLocked(entry); err != nil {
		return Entry{}, err
	}
	current.State = ReservationReleased
	current.TerminalEventID = command.EventID
	return entry, nil
}

func (a *Account) Compensate(command CompensateCommand) (Entry, error) {
	return a.resolve(command.EventID, command.ReservationID, command.Usage, command.OccurredAt, EntryCompensation, ReservationCompensated)
}

func (a *Account) Snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	reservations := make([]ReservationSnapshot, 0, len(a.reservations))
	for _, current := range a.reservations {
		reservations = append(reservations, snapshotReservation(current))
	}
	sort.Slice(reservations, func(i, j int) bool { return reservations[i].ID < reservations[j].ID })
	return Snapshot{
		UserID: a.userID, EntitlementID: a.entitlementID, Balance: a.balance,
		Revision: uint64(len(a.entries)), Reservations: reservations,
	}
}

func (a *Account) Entries() []Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Entry(nil), a.entries...)
}

func (a *Account) resolve(eventID EventID, reservationID ReservationID, usage Usage, occurredAt time.Time, kind EntryKind, state ReservationState) (Entry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := validateEvent(eventID, occurredAt); err != nil {
		return Entry{}, err
	}
	current, exists := a.reservations[reservationID]
	if !exists {
		return Entry{}, newError(ErrorReservationMissing, "reservation was not found", eventID, reservationID)
	}
	usageDecision, err := DecideUsage(usage)
	if err != nil {
		return Entry{}, err
	}
	if usageDecision.Disposition == UsageHold {
		return Entry{}, newError(ErrorUsageUnknown, "unknown usage keeps the reservation held", eventID, reservationID)
	}
	delta := current.ReservedTokens - usageDecision.ChargeTokens
	entry := terminalEntry(a, eventID, current, kind, delta, usage, occurredAt)
	if existing, handled, err := a.idempotentEntryLocked(entry); handled {
		return existing, err
	}
	if current.State != ReservationReserved {
		return Entry{}, newError(ErrorReservationState, "reservation is already terminal", eventID, reservationID)
	}
	if err := a.appendLocked(entry); err != nil {
		return Entry{}, err
	}
	current.State = state
	current.ChargedTokens = usageDecision.ChargeTokens
	current.UsageSource = usageDecision.Source
	current.TerminalEventID = eventID
	return entry, nil
}

func terminalEntry(account *Account, eventID EventID, current *reservation, kind EntryKind, delta Tokens, usage Usage, occurredAt time.Time) Entry {
	return Entry{
		ID: eventID, Kind: kind, UserID: account.userID, EntitlementID: account.entitlementID,
		ReservationID: current.ID, RequestID: current.RequestID, TokenDelta: delta,
		ReservedTokens: current.ReservedTokens, InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
		UsageSource: usage.Source, OccurredAt: occurredAt.UTC(),
	}
}

func (a *Account) idempotentEntryLocked(candidate Entry) (Entry, bool, error) {
	existing, found := a.events[candidate.ID]
	if !found {
		return Entry{}, false, nil
	}
	candidate.OccurredAt = existing.OccurredAt
	if existing != candidate {
		return Entry{}, true, newError(ErrorEventConflict, "event ID is already bound to different ledger facts", candidate.ID, candidate.ReservationID)
	}
	return existing, true, nil
}

func (a *Account) appendLocked(entry Entry) error {
	next, ok := addTokens(a.balance, entry.TokenDelta)
	if !ok {
		return newError(ErrorArithmeticOverflow, "ledger balance overflowed", entry.ID, entry.ReservationID)
	}
	a.balance = next
	a.entries = append(a.entries, entry)
	a.events[entry.ID] = entry
	return nil
}

func snapshotReservation(current *reservation) ReservationSnapshot {
	return ReservationSnapshot{
		ID: current.ID, RequestID: current.RequestID, State: current.State,
		ReservedTokens: current.ReservedTokens, ChargedTokens: current.ChargedTokens,
		UsageSource: current.UsageSource, ReserveEventID: current.ReserveEventID, TerminalEventID: current.TerminalEventID,
	}
}

func validateEvent(eventID EventID, occurredAt time.Time) error {
	if eventID == "" || occurredAt.IsZero() {
		return newError(ErrorInvalidInput, "event ID and occurrence time are required", eventID, "")
	}
	return nil
}
