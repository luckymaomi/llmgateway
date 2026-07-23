package ledger

import (
	"errors"
	"testing"
	"time"
)

var ledgerTestTime = time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

func newFundedAccount(t *testing.T, tokens Tokens) *Account {
	t.Helper()
	account, err := NewAccount("user", "subscription")
	if err != nil {
		t.Fatalf("NewAccount() error = %v", err)
	}
	if _, err := account.Grant(GrantCommand{EventID: "grant", Tokens: tokens, OccurredAt: ledgerTestTime}); err != nil {
		t.Fatalf("Grant() error = %v", err)
	}
	return account
}

func reserve(t *testing.T, account *Account, eventID EventID, reservationID ReservationID, tokens Tokens) Entry {
	t.Helper()
	entry, err := account.Reserve(ReserveCommand{
		EventID: eventID, ReservationID: reservationID, RequestID: RequestID("request-" + string(reservationID)),
		Tokens: tokens, OccurredAt: ledgerTestTime.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Reserve() error = %v", err)
	}
	return entry
}

func reservationFor(t *testing.T, snapshot Snapshot, reservationID ReservationID) ReservationSnapshot {
	t.Helper()
	for _, reservation := range snapshot.Reservations {
		if reservation.ID == reservationID {
			return reservation
		}
	}
	t.Fatalf("reservation %q was not found", reservationID)
	return ReservationSnapshot{}
}

func ledgerErrorKind(t *testing.T, err error) ErrorKind {
	t.Helper()
	var ledgerError *Error
	if !errors.As(err, &ledgerError) {
		t.Fatalf("error = %v, want *ledger.Error", err)
	}
	return ledgerError.Kind
}

func TestAccountReservesAndSettlesAuthoritativeUsage(t *testing.T) {
	account := newFundedAccount(t, 1_000)
	reserved := reserve(t, account, "reserve", "reservation", 300)
	if reserved.TokenDelta != -300 || reserved.UsageSource != UsageEstimated {
		t.Fatalf("reserve entry = %#v", reserved)
	}

	settled, err := account.Settle(SettleCommand{
		EventID: "settle", ReservationID: "reservation",
		Usage:      Usage{InputTokens: 100, OutputTokens: 50, Source: UsageAuthoritative},
		OccurredAt: ledgerTestTime.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	if settled.TokenDelta != 150 || settled.UsageSource != UsageAuthoritative {
		t.Fatalf("settlement entry = %#v", settled)
	}
	snapshot := account.Snapshot()
	if snapshot.Balance != 850 {
		t.Fatalf("balance = %d, want 850", snapshot.Balance)
	}
	reservation := reservationFor(t, snapshot, "reservation")
	if reservation.State != ReservationSettled || reservation.ChargedTokens != 150 || reservation.UsageSource != UsageAuthoritative {
		t.Fatalf("reservation = %#v", reservation)
	}
}

func TestAccountReleaseRestoresTheWholeReservation(t *testing.T) {
	account := newFundedAccount(t, 1_000)
	reserve(t, account, "reserve", "reservation", 300)
	entry, err := account.Release(ReleaseCommand{
		EventID: "release", ReservationID: "reservation", OccurredAt: ledgerTestTime.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if entry.TokenDelta != 300 || entry.UsageSource != UsageUnknown {
		t.Fatalf("release entry = %#v", entry)
	}
	if snapshot := account.Snapshot(); snapshot.Balance != 1_000 || reservationFor(t, snapshot, "reservation").State != ReservationReleased {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestAccountCompensationChargesKnownPartialUsage(t *testing.T) {
	account := newFundedAccount(t, 1_000)
	reserve(t, account, "reserve", "reservation", 300)
	entry, err := account.Compensate(CompensateCommand{
		EventID: "compensate", ReservationID: "reservation",
		Usage:      Usage{InputTokens: 30, OutputTokens: 10, Source: UsageEstimated},
		OccurredAt: ledgerTestTime.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("Compensate() error = %v", err)
	}
	if entry.TokenDelta != 260 {
		t.Fatalf("compensation delta = %d, want 260", entry.TokenDelta)
	}
	snapshot := account.Snapshot()
	reservation := reservationFor(t, snapshot, "reservation")
	if snapshot.Balance != 960 || reservation.State != ReservationCompensated || reservation.ChargedTokens != 40 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestUnknownUsageKeepsReservationHeldUntilCompensation(t *testing.T) {
	account := newFundedAccount(t, 1_000)
	reserve(t, account, "reserve", "reservation", 300)
	decision, err := DecideUsage(Usage{Source: UsageUnknown})
	if err != nil {
		t.Fatalf("DecideUsage() error = %v", err)
	}
	if decision.Disposition != UsageHold || decision.Source != UsageUnknown {
		t.Fatalf("usage decision = %#v", decision)
	}

	_, err = account.Settle(SettleCommand{
		EventID: "unknown", ReservationID: "reservation", Usage: Usage{Source: UsageUnknown},
		OccurredAt: ledgerTestTime.Add(2 * time.Minute),
	})
	if kind := ledgerErrorKind(t, err); kind != ErrorUsageUnknown {
		t.Fatalf("error kind = %q, want %q", kind, ErrorUsageUnknown)
	}
	held := account.Snapshot()
	if held.Balance != 700 || reservationFor(t, held, "reservation").State != ReservationReserved {
		t.Fatalf("held snapshot = %#v", held)
	}

	_, err = account.Compensate(CompensateCommand{
		EventID: "resolved", ReservationID: "reservation", Usage: Usage{Source: UsageEstimated},
		OccurredAt: ledgerTestTime.Add(3 * time.Minute),
	})
	if err != nil {
		t.Fatalf("Compensate(resolved) error = %v", err)
	}
	if snapshot := account.Snapshot(); snapshot.Balance != 1_000 {
		t.Fatalf("resolved snapshot = %#v", snapshot)
	}
}

func TestAccountRecordsUsageAboveReservationAsAnAuthoritativeDeficit(t *testing.T) {
	account := newFundedAccount(t, 100)
	reserve(t, account, "reserve", "reservation", 100)
	entry, err := account.Settle(SettleCommand{
		EventID: "settle", ReservationID: "reservation",
		Usage:      Usage{InputTokens: 80, OutputTokens: 70, Source: UsageAuthoritative},
		OccurredAt: ledgerTestTime.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	if entry.TokenDelta != -50 || account.Snapshot().Balance != -50 {
		t.Fatalf("entry = %#v, snapshot = %#v", entry, account.Snapshot())
	}
}

func TestAccountReturnsTheOriginalEventForAnIdempotentRetry(t *testing.T) {
	account := newFundedAccount(t, 100)
	command := ReserveCommand{
		EventID: "reserve", ReservationID: "reservation", RequestID: "request", Tokens: 50,
		OccurredAt: ledgerTestTime.Add(time.Minute),
	}
	first, err := account.Reserve(command)
	if err != nil {
		t.Fatalf("first Reserve() error = %v", err)
	}
	command.OccurredAt = ledgerTestTime.Add(10 * time.Minute)
	second, err := account.Reserve(command)
	if err != nil {
		t.Fatalf("idempotent Reserve() error = %v", err)
	}
	if second != first || len(account.Entries()) != 2 {
		t.Fatalf("first = %#v, second = %#v, entries = %#v", first, second, account.Entries())
	}
}

func TestRebuildRecreatesBalanceAndReservationProjection(t *testing.T) {
	account := newFundedAccount(t, 1_000)
	reserve(t, account, "reserve", "reservation", 300)
	if _, err := account.Settle(SettleCommand{
		EventID: "settle", ReservationID: "reservation",
		Usage:      Usage{InputTokens: 100, OutputTokens: 50, Source: UsageAuthoritative},
		OccurredAt: ledgerTestTime.Add(2 * time.Minute),
	}); err != nil {
		t.Fatalf("Settle() error = %v", err)
	}

	rebuilt, err := Rebuild("user", "subscription", account.Entries())
	if err != nil {
		t.Fatalf("Rebuild() error = %v", err)
	}
	got := rebuilt.Snapshot()
	want := account.Snapshot()
	if got.Balance != want.Balance || got.Revision != want.Revision || len(got.Reservations) != 1 || got.Reservations[0] != want.Reservations[0] {
		t.Fatalf("rebuilt = %#v, want %#v", got, want)
	}
}

func TestRebuildProtectsEventIdentityDuringRecovery(t *testing.T) {
	account := newFundedAccount(t, 100)
	entries := account.Entries()
	entries = append(entries, entries[0])

	_, err := Rebuild("user", "subscription", entries)
	if kind := ledgerErrorKind(t, err); kind != ErrorInvalidHistory {
		t.Fatalf("error kind = %q, want %q", kind, ErrorInvalidHistory)
	}
}
