package controlapi

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/httpserver"
	"github.com/luckymaomi/llmgateway/internal/identity"
)

func identityMutationRequest(w http.ResponseWriter, r *http.Request) (identity.MutationRequest, bool) {
	idempotencyKey, err := uuid.Parse(r.Header.Get("Idempotency-Key"))
	if err != nil || idempotencyKey == uuid.Nil {
		writeProblem(w, r, problem{Status: http.StatusBadRequest, Code: "invalid_idempotency_key", Message: "Idempotency-Key must be a UUID.", Stage: "identity"})
		return identity.MutationRequest{}, false
	}
	requestID := httpserver.RequestIDFromContext(r.Context())
	if requestID == "" {
		writeProblem(w, r, problem{Status: http.StatusInternalServerError, Code: "internal_invariant", Message: "Request identity is unavailable.", Stage: "identity", Retryable: true})
		return identity.MutationRequest{}, false
	}
	return identity.MutationRequest{IdempotencyKey: idempotencyKey, RequestID: requestID}, true
}
