package publicapi

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/canonical"
	"github.com/luckymaomi/llmgateway/internal/httpserver"
	"github.com/luckymaomi/llmgateway/internal/protocol"
	"github.com/luckymaomi/llmgateway/internal/requestflow"
)

func (a *API) streamResponse(w http.ResponseWriter, r *http.Request, request protocol.ResponsesRequest, command requestflow.ChatCommand, previousResponseID *uuid.UUID) {
	requestID := httpserver.RequestIDFromContext(r.Context())
	flusher, ok := w.(http.Flusher)
	if !ok {
		protocol.WriteError(w, requestID, &canonical.Error{Kind: canonical.ErrorInternalInvariant, Code: "streaming_unavailable", Message: "HTTP streaming is unavailable"})
		return
	}
	streamContext, cancel := context.WithCancel(r.Context())
	defer cancel()
	responseRecordID := uuid.New()
	state := &responseStream{writer: w, flusher: flusher, request: request, responseRecordID: responseRecordID, model: request.Chat.Model}
	sink := func(gatewayRequestID uuid.UUID, event canonical.StreamEvent) error {
		if !state.started {
			state.requestID = gatewayRequestID
			state.responseID = protocol.ResponseIdentifierForRequest(responseRecordID.String())
			if request.Store {
				if err := a.responses.Begin(streamContext, responseRecordID, gatewayRequestID, command.Principal.KeyID, previousResponseID, request.Input); err != nil {
					return err
				}
			}
			a.running.Store(responseRecordID, cancel)
			if err := state.start(event); err != nil {
				return err
			}
		}
		return state.consume(event, func(output json.RawMessage) error {
			if !request.Store {
				return nil
			}
			return a.responses.Complete(context.WithoutCancel(streamContext), responseRecordID, output)
		})
	}
	workflowError := a.workflow.Stream(streamContext, command, sink)
	if state.requestID != uuid.Nil {
		a.running.Delete(responseRecordID)
	}
	if workflowError == nil {
		return
	}
	if !state.started {
		protocol.WriteError(w, requestID, workflowError)
		return
	}
	errorBody, _ := json.Marshal(map[string]any{"code": workflowError.Code, "message": workflowError.Message})
	if request.Store {
		_ = a.responses.Fail(context.WithoutCancel(streamContext), responseRecordID, errorBody)
	}
	_ = state.emit("response.failed", map[string]any{
		"type": "response.failed", "response": protocol.PresentResponseFailed(state.responseID, state.model, state.createdAt, request, workflowError),
	})
}
