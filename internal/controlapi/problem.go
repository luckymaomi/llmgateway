package controlapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/luckymaomi/llmgateway/internal/httpserver"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/registry"
	"github.com/luckymaomi/llmgateway/internal/subscription"
)

type problem struct {
	Status      int               `json:"status"`
	Code        string            `json:"code"`
	Message     string            `json:"message"`
	Retryable   bool              `json:"retryable"`
	Stage       string            `json:"stage,omitempty"`
	RequestID   string            `json:"requestId,omitempty"`
	FieldErrors map[string]string `json:"fieldErrors,omitempty"`
}

type problemEnvelope struct {
	Error problem `json:"error"`
}

func writeProblem(w http.ResponseWriter, r *http.Request, value problem) {
	value.RequestID = httpserver.RequestIDFromContext(r.Context())
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(value.Status)
	_ = json.NewEncoder(w).Encode(problemEnvelope{Error: value})
}

func (a *API) writeRegistryError(w http.ResponseWriter, r *http.Request, err error) {
	value := problem{Status: http.StatusInternalServerError, Code: "internal_error", Message: "Registry operation failed.", Retryable: true, Stage: "registry"}
	switch {
	case errors.Is(err, registry.ErrInvalidInput):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusBadRequest, "invalid_request", "Registry input is invalid.", false
	case errors.Is(err, registry.ErrForbidden):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusForbidden, "forbidden", "The current session cannot manage the registry.", false
	case errors.Is(err, registry.ErrNotFound):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusNotFound, "not_found", "Registry record was not found.", false
	case errors.Is(err, registry.ErrConflict):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusConflict, "conflict", "Registry facts changed.", false
	case errors.Is(err, registry.ErrIdempotencyConflict):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusConflict, "idempotency_conflict", "Idempotency-Key was already used for different registry input.", false
	case errors.Is(err, registry.ErrOutcomeUnknown):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusServiceUnavailable, "operation_outcome_unknown", "The resource operation may have committed. Retry with the same Idempotency-Key.", true
	default:
		a.logFailure("registry operation failed", r, err)
	}
	writeProblem(w, r, value)
}

func (a *API) writeIdentityError(w http.ResponseWriter, r *http.Request, err error) {
	value := problem{Status: http.StatusInternalServerError, Code: "internal_error", Message: "Identity operation failed.", Retryable: true, Stage: "identity"}
	switch {
	case errors.Is(err, identity.ErrInvalidInput):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusBadRequest, "invalid_request", "Identity input is invalid.", false
	case errors.Is(err, identity.ErrInvalidCredential):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusUnauthorized, "invalid_credential", "Authentication failed.", false
	case errors.Is(err, identity.ErrDisabled):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusForbidden, "account_disabled", "The account is disabled.", false
	case errors.Is(err, identity.ErrForbidden):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusForbidden, "forbidden", "The current session cannot perform this operation.", false
	case errors.Is(err, identity.ErrConflict):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusConflict, "conflict", "Identity facts changed.", false
	case errors.Is(err, identity.ErrIdempotencyConflict):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusConflict, "idempotency_conflict", "Idempotency-Key was already used for different identity input.", false
	case errors.Is(err, identity.ErrOutcomeUnknown):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusServiceUnavailable, "operation_outcome_unknown", "The identity operation may have committed. Retry with the same Idempotency-Key.", true
	case errors.Is(err, identity.ErrNotFound):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusNotFound, "not_found", "Identity record was not found.", false
	default:
		a.logFailure("identity operation failed", r, err)
	}
	writeProblem(w, r, value)
}

func (a *API) writeSubscriptionError(w http.ResponseWriter, r *http.Request, err error) {
	value := problem{Status: http.StatusInternalServerError, Code: "internal_error", Message: "Subscription operation failed.", Retryable: true, Stage: "subscription"}
	switch {
	case errors.Is(err, subscription.ErrInvalidInput):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusBadRequest, "invalid_request", "Plan or subscription input is invalid.", false
	case errors.Is(err, subscription.ErrForbidden):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusForbidden, "forbidden", "The current session cannot perform this subscription operation.", false
	case errors.Is(err, subscription.ErrNotFound):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusNotFound, "not_found", "Plan or subscription was not found.", false
	case errors.Is(err, subscription.ErrConflict):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusConflict, "conflict", "Plan or subscription facts changed.", false
	case errors.Is(err, subscription.ErrIdempotencyConflict):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusConflict, "idempotency_conflict", "Idempotency-Key was already used for different subscription input.", false
	case errors.Is(err, subscription.ErrOutcomeUnknown):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusServiceUnavailable, "operation_outcome_unknown", "The subscription operation may have committed. Retry with the same Idempotency-Key.", true
	default:
		a.logFailure("subscription operation failed", r, err)
	}
	writeProblem(w, r, value)
}

func (a *API) logFailure(message string, r *http.Request, err error) {
	if a.logger != nil {
		a.logger.Error(message, "request_id", httpserver.RequestIDFromContext(r.Context()), "error", err)
	}
}
