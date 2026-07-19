package ledger

import "fmt"

func Rebuild(userID UserID, entitlementID EntitlementID, entries []Entry) (*Account, error) {
	account, err := NewAccount(userID, entitlementID)
	if err != nil {
		return nil, err
	}
	seen := make(map[EventID]struct{}, len(entries))
	for index, entry := range entries {
		if _, exists := seen[entry.ID]; exists {
			return nil, newError(ErrorInvalidHistory, fmt.Sprintf("ledger entry %d repeats an event ID", index), entry.ID, entry.ReservationID)
		}
		seen[entry.ID] = struct{}{}
		applied, applyError := replayEntry(account, entry)
		if applyError != nil {
			return nil, newError(ErrorInvalidHistory, fmt.Sprintf("ledger entry %d is invalid: %v", index, applyError), entry.ID, entry.ReservationID)
		}
		if applied != entry {
			return nil, newError(ErrorInvalidHistory, fmt.Sprintf("ledger entry %d does not match its derived facts", index), entry.ID, entry.ReservationID)
		}
	}
	return account, nil
}

func replayEntry(account *Account, entry Entry) (Entry, error) {
	switch entry.Kind {
	case EntryGrant:
		return account.Grant(GrantCommand{EventID: entry.ID, Tokens: entry.TokenDelta, OccurredAt: entry.OccurredAt})
	case EntryReserve:
		return account.Reserve(ReserveCommand{
			EventID: entry.ID, ReservationID: entry.ReservationID, RequestID: entry.RequestID,
			Tokens: entry.ReservedTokens, OccurredAt: entry.OccurredAt,
		})
	case EntrySettlement:
		return account.Settle(SettleCommand{
			EventID: entry.ID, ReservationID: entry.ReservationID,
			Usage:      Usage{InputTokens: entry.InputTokens, OutputTokens: entry.OutputTokens, Source: entry.UsageSource},
			OccurredAt: entry.OccurredAt,
		})
	case EntryRelease:
		return account.Release(ReleaseCommand{EventID: entry.ID, ReservationID: entry.ReservationID, OccurredAt: entry.OccurredAt})
	case EntryCompensation:
		return account.Compensate(CompensateCommand{
			EventID: entry.ID, ReservationID: entry.ReservationID,
			Usage:      Usage{InputTokens: entry.InputTokens, OutputTokens: entry.OutputTokens, Source: entry.UsageSource},
			OccurredAt: entry.OccurredAt,
		})
	default:
		return Entry{}, newError(ErrorInvalidHistory, "ledger entry kind is invalid", entry.ID, entry.ReservationID)
	}
}
