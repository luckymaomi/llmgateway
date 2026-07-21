package execution

import (
	"errors"

	"github.com/google/uuid"
)

var (
	ErrFenced       = errors.New("request execution is fenced")
	ErrNotClaimable = errors.New("request execution is not claimable")
)

type Claim struct {
	RequestID   uuid.UUID
	ExecutionID uuid.UUID
	Generation  int64
}

func (c Claim) Valid() bool {
	return c.RequestID != uuid.Nil && c.ExecutionID != uuid.Nil && c.Generation > 0
}
