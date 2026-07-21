package publicapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/canonical"
	"github.com/luckymaomi/llmgateway/internal/httpserver"
	"github.com/luckymaomi/llmgateway/internal/protocol"
	"github.com/luckymaomi/llmgateway/internal/requestflow"
	responseowner "github.com/luckymaomi/llmgateway/internal/responses"
)

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
	principal := principalFromContext(r.Context())
	previousResponseID, previousMessages, previousError := a.previousResponseMessages(r.Context(), request.PreviousResponseID, principal.KeyID)
	if previousError != nil {
		protocol.WriteError(w, requestID, previousError)
		return
	}
	if len(previousMessages) > 0 {
		request.Chat.Messages = append(previousMessages, request.Chat.Messages...)
	}
	idempotencyKey, keyError := parseIdempotencyKey(r.Header.Get("Idempotency-Key"))
	if keyError != nil {
		protocol.WriteError(w, requestID, keyError)
		return
	}
	digest := sha256.Sum256(body)
	if request.Background {
		if !request.Store {
			protocol.WriteError(w, requestID, &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "background_requires_store", Message: "background Responses must be stored", Parameter: "store", HTTPStatus: http.StatusBadRequest})
			return
		}
		responseRecordID := uuid.New()
		stored, err := a.responses.Enqueue(r.Context(), responseRecordID, principal.KeyID, previousResponseID, idempotencyKey, digest[:], request.Input, body)
		if err != nil {
			if errors.Is(err, responseowner.ErrConflict) || errors.Is(err, responseowner.ErrNotFound) {
				protocol.WriteError(w, requestID, &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "idempotency_conflict", Message: "Idempotency-Key was reused with a different request", HTTPStatus: http.StatusConflict})
				return
			}
			protocol.WriteError(w, requestID, &canonical.Error{Kind: canonical.ErrorStorageUnavailable, Code: "response_persistence_failed", Message: "background response could not be accepted", Cause: err})
			return
		}
		a.wakeResponseWorker()
		writeStoredResponse(w, stored, request)
		return
	}
	command := requestflow.ChatCommand{Principal: principal, Request: request.Chat, RequestDigest: digest[:], IdempotencyKey: idempotencyKey}
	if request.Chat.Stream {
		a.streamResponse(w, r, request, command, previousResponseID)
		return
	}
	result, workflowError := a.workflow.Chat(r.Context(), command)
	if workflowError != nil {
		protocol.WriteError(w, requestID, workflowError)
		return
	}
	responseRecordID := uuid.New()
	responseID := protocol.ResponseIdentifierForRequest(responseRecordID.String())
	presented := protocol.PresentResponseWithID(responseID, result.Response, request)
	if request.Store {
		encoded, err := json.Marshal(presented)
		if err != nil || a.responses.SaveCompleted(r.Context(), responseRecordID, result.RequestID, command.Principal.KeyID, previousResponseID, request.Input, encoded) != nil {
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
	if a.responses == nil || errors.Is(a.responses.Delete(r.Context(), responseID, principal.KeyID), responseowner.ErrNotFound) {
		writeResponseNotFound(w, r)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"id": protocol.ResponseIdentifierForRequest(responseID.String()), "object": "response", "deleted": true})
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
	stored, err := a.responses.Get(r.Context(), responseID, principal.KeyID)
	if err != nil {
		writeResponseNotFound(w, r)
		return
	}
	items, itemError := responseInputItems(stored.ID, stored.Input)
	if itemError != nil {
		protocol.WriteError(w, httpserver.RequestIDFromContext(r.Context()), &canonical.Error{Kind: canonical.ErrorInternalInvariant, Code: "stored_input_invalid", Message: "stored response input is invalid"})
		return
	}
	page, pageError := paginateResponseItems(r, items)
	if pageError != nil {
		protocol.WriteError(w, httpserver.RequestIDFromContext(r.Context()), pageError)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, page)
}

func (a *API) cancelResponse(w http.ResponseWriter, r *http.Request) {
	responseID, ok := parseResponseID(w, r)
	if !ok {
		return
	}
	principal := principalFromContext(r.Context())
	if a.responses == nil {
		writeResponseNotFound(w, r)
		return
	}
	if err := a.responses.RequestCancellation(r.Context(), responseID, principal.KeyID); err != nil {
		switch {
		case errors.Is(err, responseowner.ErrNotFound):
			writeResponseNotFound(w, r)
		case errors.Is(err, responseowner.ErrNotCancelable):
			protocol.WriteError(w, httpserver.RequestIDFromContext(r.Context()), &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "response_not_cancelable", Message: "only background Responses can be canceled", HTTPStatus: http.StatusBadRequest})
		default:
			protocol.WriteError(w, httpserver.RequestIDFromContext(r.Context()), &canonical.Error{Kind: canonical.ErrorStorageUnavailable, Code: "response_cancel_failed", Message: "response cancellation could not be persisted", Cause: err})
		}
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
	stored, err := a.responses.Get(r.Context(), responseID, principal.KeyID)
	if err != nil {
		writeResponseNotFound(w, r)
		return
	}
	if stored.Status == responseowner.StatusCompleted && len(stored.Output) > 0 {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(stored.Output)
		return
	}
	status := string(stored.Status)
	if allowCanceled && status != "completed" && status != "failed" {
		status = "canceled"
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"id": protocol.ResponseIdentifierForRequest(responseID.String()), "object": "response", "status": status, "output": []any{}, "error": rawJSONOrNil(stored.Error)})
}

func writeStoredResponse(w http.ResponseWriter, stored responseowner.Record, request protocol.ResponsesRequest) {
	if stored.Status == responseowner.StatusCompleted && len(stored.Output) > 0 {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(stored.Output)
		return
	}
	responseID := protocol.ResponseIdentifierForRequest(stored.ID.String())
	if stored.Status == responseowner.StatusQueued {
		httpserver.WriteJSON(w, http.StatusOK, protocol.PresentResponseQueued(responseID, request.Chat.Model, stored.CreatedAt.Unix(), request))
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, map[string]any{"id": responseID, "object": "response", "status": stored.Status, "output": []any{}, "error": rawJSONOrNil(stored.Error)})
}

func parseResponseID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	responseID, err := parseResponseIdentifier(chi.URLParam(r, "responseID"))
	if err != nil {
		writeResponseNotFound(w, r)
		return uuid.Nil, false
	}
	return responseID, true
}

func parseResponseIdentifier(value string) (uuid.UUID, error) {
	if !strings.HasPrefix(value, "resp_") {
		return uuid.Nil, errors.New("response ID must start with resp_")
	}
	return uuid.Parse(strings.TrimPrefix(value, "resp_"))
}

func (a *API) previousResponseMessages(ctx context.Context, externalID string, gatewayKeyID uuid.UUID) (*uuid.UUID, []canonical.Message, *canonical.Error) {
	if externalID == "" {
		return nil, nil, nil
	}
	responseID, err := parseResponseIdentifier(externalID)
	if err != nil {
		return nil, nil, &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "response_not_found", Message: "previous response was not found", Parameter: "previous_response_id", HTTPStatus: http.StatusNotFound}
	}
	chain := make([]responseowner.Record, 0, 4)
	currentID := responseID
	for len(chain) < 100 {
		record, readError := a.responses.Get(ctx, currentID, gatewayKeyID)
		if readError != nil {
			return nil, nil, &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "response_not_found", Message: "previous response was not found", Parameter: "previous_response_id", HTTPStatus: http.StatusNotFound}
		}
		if record.Status != responseowner.StatusCompleted || len(record.Output) == 0 {
			return nil, nil, &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "previous_response_not_ready", Message: "previous response is not completed", Parameter: "previous_response_id", HTTPStatus: http.StatusConflict}
		}
		chain = append(chain, record)
		if record.PreviousResponseID == nil {
			break
		}
		currentID = *record.PreviousResponseID
	}
	if len(chain) == 100 && chain[len(chain)-1].PreviousResponseID != nil {
		return nil, nil, &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "response_chain_too_long", Message: "previous response chain exceeds the supported length", Parameter: "previous_response_id", HTTPStatus: http.StatusBadRequest}
	}
	slices.Reverse(chain)
	messages := make([]canonical.Message, 0, len(chain)*2)
	for _, record := range chain {
		recordMessages, parseError := protocol.StoredResponseMessages(record.Input, record.Output)
		if parseError != nil {
			return nil, nil, &canonical.Error{Kind: canonical.ErrorInternalInvariant, Code: "stored_response_invalid", Message: "previous response state is invalid", Cause: parseError}
		}
		messages = append(messages, recordMessages...)
	}
	return &responseID, messages, nil
}

func responseInputItems(responseID uuid.UUID, input json.RawMessage) ([]map[string]any, error) {
	var rawItems []map[string]any
	var text string
	if json.Unmarshal(input, &text) == nil {
		rawItems = []map[string]any{{"type": "message", "role": "user", "content": []map[string]any{{"type": "input_text", "text": text}}}}
	} else if err := json.Unmarshal(input, &rawItems); err != nil {
		return nil, err
	}
	for index, item := range rawItems {
		if _, exists := item["id"]; !exists {
			item["id"] = fmt.Sprintf("item_%s_%d", strings.ReplaceAll(responseID.String(), "-", ""), index)
		}
	}
	return rawItems, nil
}

func paginateResponseItems(r *http.Request, items []map[string]any) (map[string]any, *canonical.Error) {
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			return nil, &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "invalid_limit", Message: "limit must be between 1 and 100", Parameter: "limit", HTTPStatus: http.StatusBadRequest}
		}
		limit = parsed
	}
	order := r.URL.Query().Get("order")
	if order == "" {
		order = "desc"
	}
	ordered := append([]map[string]any(nil), items...)
	if order == "desc" {
		slices.Reverse(ordered)
	} else if order != "asc" {
		return nil, &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "invalid_order", Message: "order must be asc or desc", Parameter: "order", HTTPStatus: http.StatusBadRequest}
	}
	if after := r.URL.Query().Get("after"); after != "" {
		found := -1
		for index, item := range ordered {
			if item["id"] == after {
				found = index
				break
			}
		}
		if found < 0 {
			return nil, &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "invalid_after", Message: "after does not identify an input item", Parameter: "after", HTTPStatus: http.StatusBadRequest}
		}
		ordered = ordered[found+1:]
	}
	hasMore := len(ordered) > limit
	if hasMore {
		ordered = ordered[:limit]
	}
	var firstID, lastID any
	if len(ordered) > 0 {
		firstID = ordered[0]["id"]
		lastID = ordered[len(ordered)-1]["id"]
	}
	return map[string]any{"object": "list", "data": ordered, "first_id": firstID, "last_id": lastID, "has_more": hasMore}, nil
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
