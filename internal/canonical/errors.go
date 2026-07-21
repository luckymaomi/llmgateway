package canonical

import (
	"fmt"
	"time"
)

type ErrorKind string

const (
	ErrorInvalidRequest        ErrorKind = "invalid_request"
	ErrorAuthentication        ErrorKind = "authentication"
	ErrorPermission            ErrorKind = "permission"
	ErrorQuota                 ErrorKind = "quota"
	ErrorAdmissionTimeout      ErrorKind = "admission_timeout"
	ErrorRateLimit             ErrorKind = "rate_limit"
	ErrorUnsupportedCapability ErrorKind = "unsupported_capability"
	ErrorProviderConfiguration ErrorKind = "provider_configuration"
	ErrorProviderTemporary     ErrorKind = "provider_temporary"
	ErrorProviderPermanent     ErrorKind = "provider_permanent"
	ErrorStreamInterrupted     ErrorKind = "stream_interrupted"
	ErrorUncertain             ErrorKind = "uncertain"
	ErrorStorageUnavailable    ErrorKind = "storage_unavailable"
	ErrorInternalInvariant     ErrorKind = "internal_invariant"
)

type RetryAfter struct {
	DelaySeconds *int64
	At           *time.Time
}

type Error struct {
	Kind         ErrorKind
	Code         string
	Message      string
	Parameter    string
	Provider     string
	ProviderType string
	RequestID    string
	HTTPStatus   int
	RetryAfter   *RetryAfter
	ReplaySafe   bool
	Cause        error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Provider != "" {
		return fmt.Sprintf("%s: %s: %s", e.Provider, e.Kind, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
