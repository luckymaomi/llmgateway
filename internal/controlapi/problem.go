package controlapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/luckymaomi/llmgateway/internal/configuration"
	"github.com/luckymaomi/llmgateway/internal/httpserver"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/registry"
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

func (a *API) unavailable(feature string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeProblem(w, r, problem{
			Status:  http.StatusNotImplemented,
			Code:    "feature_not_implemented",
			Message: "This control-plane capability does not have a runtime owner yet.",
			Stage:   feature,
		})
	}
}

func (a *API) writeConfigurationError(w http.ResponseWriter, r *http.Request, err error) {
	value := problem{Status: http.StatusInternalServerError, Code: "internal_error", Message: "Configuration operation failed.", Retryable: true, Stage: "configuration"}
	switch {
	case errors.Is(err, configuration.ErrInvalidInput):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusBadRequest, "invalid_configuration", "Configuration is invalid.", false
	case errors.Is(err, configuration.ErrForbidden):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusForbidden, "forbidden", "The current session cannot manage configuration.", false
	case errors.Is(err, configuration.ErrNotFound):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusNotFound, "not_found", "Configuration revision was not found.", false
	case errors.Is(err, configuration.ErrConflict):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusConflict, "configuration_conflict", "The active configuration changed.", false
	default:
		a.logFailure("configuration operation failed", r, err)
	}
	writeProblem(w, r, value)
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
	default:
		a.logFailure("registry operation failed", r, err)
	}
	writeProblem(w, r, value)
}

func (a *API) writeRegistrySnapshotError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, configuration.ErrInvalidInput) || errors.Is(err, configuration.ErrForbidden) || errors.Is(err, configuration.ErrNotFound) || errors.Is(err, configuration.ErrConflict) {
		a.writeConfigurationError(w, r, err)
		return
	}
	a.writeRegistryError(w, r, err)
}

func (a *API) writeIdentityError(w http.ResponseWriter, r *http.Request, err error) {
	value := problem{Status: http.StatusInternalServerError, Code: "internal_error", Message: "Identity operation failed.", Retryable: true, Stage: "identity"}
	switch {
	case errors.Is(err, identity.ErrInvalidInput):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusBadRequest, "invalid_request", "Identity input is invalid.", false
	case errors.Is(err, identity.ErrInvalidCredential):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusUnauthorized, "invalid_credential", "Authentication failed.", false
	case errors.Is(err, identity.ErrApprovalRequired):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusForbidden, "approval_required", "The account is awaiting approval.", false
	case errors.Is(err, identity.ErrDisabled):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusForbidden, "account_disabled", "The account is disabled.", false
	case errors.Is(err, identity.ErrForbidden):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusForbidden, "forbidden", "The current session cannot perform this operation.", false
	case errors.Is(err, identity.ErrInvalidInvitation):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusConflict, "invitation_unavailable", "The invitation cannot be claimed.", false
	case errors.Is(err, identity.ErrConflict):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusConflict, "conflict", "Identity facts changed.", false
	case errors.Is(err, identity.ErrNotFound):
		value.Status, value.Code, value.Message, value.Retryable = http.StatusNotFound, "not_found", "Identity record was not found.", false
	default:
		a.logFailure("identity operation failed", r, err)
	}
	writeProblem(w, r, value)
}

func (a *API) logFailure(message string, r *http.Request, err error) {
	if a.logger != nil {
		a.logger.Error(message, "request_id", httpserver.RequestIDFromContext(r.Context()), "error", err)
	}
}
