package responses

import (
	"errors"
	"testing"
)

func TestRecoveryOwnershipLossIsAnIdempotentMultiInstanceRace(t *testing.T) {
	for _, err := range []error{ErrConflict, ErrNotFound, errors.Join(errors.New("store"), ErrNotFound)} {
		if !recoveryOwnershipLost(err) {
			t.Fatalf("recoveryOwnershipLost(%v) = false", err)
		}
	}
	if recoveryOwnershipLost(ErrFenced) {
		t.Fatal("execution fencing must remain actionable")
	}
}
