package publicapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/canonical"
	"github.com/luckymaomi/llmgateway/internal/httpserver"
	"github.com/luckymaomi/llmgateway/internal/protocol"
	"github.com/luckymaomi/llmgateway/internal/requestflow"
)

var ErrResponseNotFound = errors.New("response not found")

func (a *API) createResponse(w http.ResponseWriter, r *http.Request) {
	requestID := httpserver.RequestIDFromContext(r.Context())
	body, err := io.ReadAll(io.LimitReader(r.Body, maxPublicRequestBytes+1))
	if err != nil || len(body) > maxPublicRequestBytes {
		protocol.WriteError(w, requestID, &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "request_too_large", Message: "request body exceeds the configured limit", HTTPStatus: http.StatusRequestEntityTooLarge})
		return
	}
	request, parseError := protocol.ParseResponsesRequest(bytes.NewReader(body), requestID)
	if parseError != nil {
		protocol.WriteError(w, requestID, parseError)
		return
	}
	if request.Store && a.responses == nil {
		protocol.WriteError(w, requestID, &canonical.Error{Kind: canonical.ErrorStorageUnavailable, Code: "response_store_unavailable", Message: "stored Responses are unavailable"})
		return
	}
	idempotencyKey, keyError := parseIdempotencyKey(r.Header.Get("Idempotency-Key"))
	if keyError != nil {
		protocol.WriteError(w, requestID, keyError)
		return
	}
	digest := sha256.Sum256(body)
	command := requestflow.ChatCommand{Principal: principalFromContext(r.Context()), Request: request.Chat, RequestDigest: digest[:], IdempotencyKey: idempotencyKey}
	if request.Chat.Stream {
		a.streamResponse(w, r, request, command)
		return
	}
	result, workflowError := a.workflow.Chat(r.Context(), command)
	if workflowError != nil {
		protocol.WriteError(w, requestID, workflowError)
		return
	}
	responseID := protocol.ResponseIdentifierForRequest(result.RequestID.String())
	presented := protocol.PresentResponseWithID(responseID, result.Response, request)
	if request.Store {
		encoded, err := json.Marshal(presented)
		if err != nil || a.responses.SaveCompleted(r.Context(), result.RequestID, command.Principal.UserID, request.Input, encoded) != nil {
			protocol.WriteError(w, requestID, &canonical.Error{Kind: canonical.ErrorStorageUnavailable, Code: "response_persistence_failed", Message: "response could not be stored", Cause: err})
			return
		}
	}
	w.Header().Set("X-Gateway-Request-ID", result.RequestID.String())
	httpserver.WriteJSON(w, http.StatusOK, presented)
}

func (a *API) getResponse(w http.ResponseWriter, r *http.Request) {
	a.readStoredResponse(w, r, false)
}

func (a *API) deleteResponse(w http.ResponseWriter, r *http.Request) {
	responseID, ok := parseResponseID(w, r)
	if !ok {
		return
	}
	principal := principalFromContext(r.Context())
	if a.responses == nil || errors.Is(a.responses.Delete(r.Context(), responseID, principal.UserID), ErrResponseNotFound) {
		writeResponseNotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) responseInputItems(w http.ResponseWriter, r *http.Request) {
	responseID, ok := parseResponseID(w, r)
	if !ok {
		return
	}
	principal := principalFromContext(r.Context())
	if a.responses == nil {
		writeResponseNotFound(w, r)
		return
	}
	stored, err := a.responses.Get(r.Context(), responseID, principal.UserID)
	if err != nil {
		writeResponseNotFound(w, r)
		return
	}
	var input any
	if err := json.Unmarshal(stored.Input, &input); err != nil {
		protocol.WriteError(w, httpserver.RequestIDFromContext(r.Context()), &canonical.Error{Kind: canonical.ErrorInternalInvariant, Code: "stored_input_invalid", Message: "stored response input is invalid"})
		return
	}
	items, ok := input.([]any)
	if !ok {
		items = []any{map[string]any{"type": "message", "role": "user", "content": input}}
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"object": "list", "data": items, "first_id": nil, "last_id": nil, "has_more": false})
}

func (a *API) cancelResponse(w http.ResponseWriter, r *http.Request) {
	responseID, ok := parseResponseID(w, r)
	if !ok {
		return
	}
	principal := principalFromContext(r.Context())
	if a.responses == nil || a.responses.RequestCancellation(r.Context(), responseID, principal.UserID) != nil {
		writeResponseNotFound(w, r)
		return
	}
	if cancelValue, found := a.running.Load(responseID); found {
		cancelValue.(context.CancelFunc)()
	}
	a.readStoredResponse(w, r, true)
}

func (a *API) readStoredResponse(w http.ResponseWriter, r *http.Request, allowCanceled bool) {
	responseID, ok := parseResponseID(w, r)
	if !ok {
		return
	}
	principal := principalFromContext(r.Context())
	if a.responses == nil {
		writeResponseNotFound(w, r)
		return
	}
	stored, err := a.responses.Get(r.Context(), responseID, principal.UserID)
	if err != nil {
		writeResponseNotFound(w, r)
		return
	}
	if len(stored.Output) > 0 {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(stored.Output)
		return
	}
	status := stored.Status
	if allowCanceled && status != "completed" && status != "failed" {
		status = "cancelled"
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"id": protocol.ResponseIdentifierForRequest(responseID.String()), "object": "response", "status": status, "output": []any{}, "error": rawJSONOrNil(stored.Error)})
}

func parseResponseID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	raw := strings.TrimPrefix(chi.URLParam(r, "responseID"), "resp_")
	responseID, err := uuid.Parse(raw)
	if err != nil {
		writeResponseNotFound(w, r)
		return uuid.Nil, false
	}
	return responseID, true
}

func writeResponseNotFound(w http.ResponseWriter, r *http.Request) {
	protocol.WriteError(w, httpserver.RequestIDFromContext(r.Context()), &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "response_not_found", Message: "response was not found", HTTPStatus: http.StatusNotFound})
}

func rawJSONOrNil(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var value any
	_ = json.Unmarshal(raw, &value)
	return value
}
