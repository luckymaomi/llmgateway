package ledger

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentReservationsNeverSpendMoreThanTheGrantedBalance(t *testing.T) {
	account := newFundedAccount(t, 1_000)
	const workers = 100
	const reservationTokens = Tokens(20)
	start := make(chan struct{})
	var completed sync.WaitGroup
	var reserved atomic.Int32
	var exhausted atomic.Int32
	completed.Add(workers)
	for index := 0; index < workers; index++ {
		go func(index int) {
			defer completed.Done()
			<-start
			id := ReservationID(fmt.Sprintf("reservation-%03d", index))
			_, err := account.Reserve(ReserveCommand{
				EventID: EventID(fmt.Sprintf("event-%03d", index)), ReservationID: id,
				RequestID: RequestID(fmt.Sprintf("request-%03d", index)), Tokens: reservationTokens,
				OccurredAt: ledgerTestTime.Add(time.Minute),
			})
			if err == nil {
				reserved.Add(1)
				return
			}
			var ledgerError *Error
			if !errors.As(err, &ledgerError) || ledgerError.Kind != ErrorInsufficientTokens {
				t.Errorf("Reserve(%d) error = %v", index, err)
				return
			}
			exhausted.Add(1)
		}(index)
	}
	close(start)
	completed.Wait()
	if reserved.Load() != 50 || exhausted.Load() != 50 {
		t.Fatalf("reserved = %d, exhausted = %d", reserved.Load(), exhausted.Load())
	}
	snapshot := account.Snapshot()
	if snapshot.Balance != 0 || len(snapshot.Reservations) != 50 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}
