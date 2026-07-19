package resilience

import "time"

type Clock interface {
	Now() time.Time
}

type Random interface {
	Int63n(limit int64) int64
}

type FailureClass string

const (
	FailureInvalidRequest    FailureClass = "invalid_request"
	FailureAuthentication    FailureClass = "authentication"
	FailurePermission        FailureClass = "permission"
	FailureQuota             FailureClass = "quota"
	FailureRateLimit         FailureClass = "rate_limit"
	FailureProviderTemporary FailureClass = "provider_temporary"
	FailureProviderPermanent FailureClass = "provider_permanent"
	FailureTimeout           FailureClass = "timeout"
	FailureNetwork           FailureClass = "network"
	FailureCanceled          FailureClass = "canceled"
	FailureStreamInterrupted FailureClass = "stream_interrupted"
	FailureStorage           FailureClass = "storage"
	FailureUncertain         FailureClass = "uncertain"
)

type SendBoundary string

const (
	SendNotStarted SendBoundary = "not_started"
	SendRejected   SendBoundary = "rejected"
	SendAccepted   SendBoundary = "accepted"
	SendUncertain  SendBoundary = "uncertain"
)

type ClientBoundary string

const (
	ClientUncommitted ClientBoundary = "uncommitted"
	ClientCommitted   ClientBoundary = "committed"
)

type RetryAfter struct {
	Delay time.Duration
	At    time.Time
}

type RetryInput struct {
	Attempt               int
	RequestStartedAt      time.Time
	Failure               FailureClass
	SendBoundary          SendBoundary
	ClientBoundary        ClientBoundary
	IdempotencyGuaranteed bool
	RetryAfter            *RetryAfter
}

type RetryAction string

const (
	RetrySchedule  RetryAction = "schedule"
	RetryStop      RetryAction = "stop"
	RetryUncertain RetryAction = "uncertain"
)

type RetryReason string

const (
	ReasonRetryableFailure RetryReason = "retryable_failure"
	ReasonTerminalFailure  RetryReason = "terminal_failure"
	ReasonAttemptBudget    RetryReason = "attempt_budget_exhausted"
	ReasonElapsedBudget    RetryReason = "elapsed_budget_exhausted"
	ReasonSendUncertain    RetryReason = "send_uncertain"
	ReasonClientCommitted  RetryReason = "client_committed"
	ReasonCanceled         RetryReason = "canceled"
)

type RetryDecision struct {
	Action        RetryAction
	Reason        RetryReason
	NextAttempt   int
	Delay         time.Duration
	NextAttemptAt time.Time
}

type ErrorKind string

const (
	ErrorInvalidConfiguration ErrorKind = "invalid_configuration"
	ErrorInvalidInput         ErrorKind = "invalid_input"
	ErrorRandomSource         ErrorKind = "random_source"
)

type Error struct {
	Kind    ErrorKind
	Message string
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return e.Message
}

func newError(kind ErrorKind, message string) *Error {
	return &Error{Kind: kind, Message: message}
}
