package publicapi

import (
	"bytes"
	"crypto/sha256"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/canonical"
	"github.com/luckymaomi/llmgateway/internal/httpserver"
	"github.com/luckymaomi/llmgateway/internal/protocol"
	"github.com/luckymaomi/llmgateway/internal/requestflow"
)

const maxPublicRequestBytes = 4 << 20

func (a *API) chatCompletions(w http.ResponseWriter, r *http.Request) {
	requestID := httpserver.RequestIDFromContext(r.Context())
	body, err := io.ReadAll(io.LimitReader(r.Body, maxPublicRequestBytes+1))
	if err != nil || len(body) > maxPublicRequestBytes {
		protocol.WriteError(w, requestID, &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "request_too_large", Message: "request body exceeds the configured limit", HTTPStatus: http.StatusRequestEntityTooLarge})
		return
	}
	request, parseError := protocol.ParseChatRequest(bytes.NewReader(body), requestID)
	if parseError != nil {
		protocol.WriteError(w, requestID, parseError)
		return
	}
	idempotencyKey, keyError := parseIdempotencyKey(r.Header.Get("Idempotency-Key"))
	if keyError != nil {
		protocol.WriteError(w, requestID, keyError)
		return
	}
	digest := sha256.Sum256(body)
	command := requestflow.ChatCommand{
		Principal: principalFromContext(r.Context()), Request: request,
		RequestDigest: digest[:], IdempotencyKey: idempotencyKey,
	}
	if request.Stream {
		a.streamChat(w, r, command)
		return
	}
	result, workflowError := a.workflow.Chat(r.Context(), command)
	if workflowError != nil {
		protocol.WriteError(w, requestID, workflowError)
		return
	}
	w.Header().Set("X-Gateway-Request-ID", result.RequestID.String())
	httpserver.WriteJSON(w, http.StatusOK, protocol.PresentChatResponse(result.Response))
}

func (a *API) streamChat(w http.ResponseWriter, r *http.Request, command requestflow.ChatCommand) {
	requestID := httpserver.RequestIDFromContext(r.Context())
	if _, ok := w.(http.Flusher); !ok {
		protocol.WriteError(w, requestID, &canonical.Error{Kind: canonical.ErrorInternalInvariant, Code: "streaming_unavailable", Message: "HTTP streaming is unavailable"})
		return
	}
	committed := false
	controller := http.NewResponseController(w)
	sink := func(logicalRequestID uuid.UUID, event canonical.StreamEvent) error {
		if !committed {
			w.Header().Set("X-Gateway-Request-ID", logicalRequestID.String())
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache, no-transform")
			w.Header().Set("X-Accel-Buffering", "no")
			w.WriteHeader(http.StatusOK)
			committed = true
		}
		var err error
		if event.Type == canonical.StreamDone {
			err = protocol.WriteSSEDone(w)
		} else {
			err = protocol.WriteSSE(w, protocol.PresentStreamEvent(event))
		}
		if err != nil {
			return err
		}
		return controller.Flush()
	}
	workflowError := a.workflow.Stream(r.Context(), command, sink)
	if workflowError == nil {
		return
	}
	if !committed {
		protocol.WriteError(w, requestID, workflowError)
		return
	}
	_ = protocol.WriteSSE(w, streamErrorEnvelope(workflowError, requestID))
	_ = protocol.WriteSSEDone(w)
	_ = controller.Flush()
}

func parseIdempotencyKey(raw string) (*string, *canonical.Error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, nil
	}
	if len(value) > 200 || strings.ContainsAny(value, "\r\n\x00") {
		return nil, &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "invalid_idempotency_key", Message: "Idempotency-Key is invalid", HTTPStatus: http.StatusBadRequest}
	}
	return &value, nil
}

func streamErrorEnvelope(providerError *canonical.Error, requestID string) map[string]any {
	return map[string]any{"error": map[string]any{
		"message": providerError.Message, "type": string(providerError.Kind), "param": providerError.Parameter, "code": providerError.Code,
	}, "request_id": requestID}
}
