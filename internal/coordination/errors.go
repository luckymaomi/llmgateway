package coordination

import "errors"

var (
	ErrInvalidInput = errors.New("coordination input is invalid")
	ErrUnavailable  = errors.New("coordination store is unavailable")
	ErrLeaseLost    = errors.New("coordination lease is no longer held")
)
