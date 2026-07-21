package controlapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/canonical"
	"github.com/luckymaomi/llmgateway/internal/httpserver"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/protocol"
	"github.com/luckymaomi/llmgateway/internal/registry"
	"github.com/luckymaomi/llmgateway/internal/requestflow"
)

type playgroundModelView struct {
	ID            string                 `json:"id"`
	Alias         string                 `json:"alias"`
	ProviderName  string                 `json:"providerName"`
	Capabilities  []string               `json:"capabilities"`
	ReasoningMode registry.ReasoningMode `json:"reasoningMode,omitempty"`
}

type playgroundRunInput struct {
	GatewayKeyID     uuid.UUID           `json:"gatewayKeyId"`
	Model            string              `json:"model"`
	Stream           bool                `json:"stream"`
	Messages         []playgroundMessage `json:"messages"`
	Tools            []playgroundTool    `json:"tools,omitempty"`
	ReasoningEnabled *bool               `json:"reasoningEnabled,omitempty"`
	ReasoningEffort  string              `json:"reasoningEffort,omitempty"`
}

type playgroundMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type playgroundTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

func (a *API) registerPlaygroundRoutes(router chi.Router) {
	router.Get("/playground/models", a.playgroundModels)
	router.With(a.requireCSRF).Post("/playground/runs", a.playgroundRun)
}

func (a *API) playgroundModels(w http.ResponseWriter, r *http.Request) {
	keyID, err := uuid.Parse(r.URL.Query().Get("gatewayKeyId"))
	if err != nil || keyID == uuid.Nil {
		writeProblem(w, r, problem{Status: http.StatusBadRequest, Code: "invalid_gateway_key", Message: "Select an active gateway Key.", Stage: "playground"})
		return
	}
	if _, err := a.playgroundPrincipal(r, keyID); err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	models, err := a.playground.Models(r.Context(), keyID)
	if err != nil {
		a.logFailure("playground model catalog failed", r, err)
		writeProblem(w, r, problem{Status: http.StatusServiceUnavailable, Code: "model_catalog_unavailable", Message: "The selected Key model catalog is unavailable.", Retryable: true, Stage: "playground"})
		return
	}
	views := make([]playgroundModelView, 0, len(models))
	for _, model := range models {
		views = append(views, playgroundModelView{
			ID: model.ID.String(), Alias: model.PublicName, ProviderName: model.ProviderSlug,
			Capabilities: playgroundCapabilities(model), ReasoningMode: model.Capabilities.ReasoningMode,
		})
	}
	writeData(w, http.StatusOK, views)
}

func (a *API) playgroundRun(w http.ResponseWriter, r *http.Request) {
	var input playgroundRunInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	principal, err := a.playgroundPrincipal(r, input.GatewayKeyID)
	if err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if _, err := uuid.Parse(idempotencyKey); err != nil {
		writeProblem(w, r, problem{Status: http.StatusBadRequest, Code: "invalid_idempotency_key", Message: "Idempotency-Key must be a UUID.", Stage: "playground"})
		return
	}
	command, parseError := playgroundCommand(r, principal, input, idempotencyKey)
	if parseError != nil {
		writeProblem(w, r, playgroundProblem(parseError, httpserver.RequestIDFromContext(r.Context())))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeProblem(w, r, problem{Status: http.StatusInternalServerError, Code: "streaming_unavailable", Message: "HTTP streaming is unavailable.", Stage: "playground"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	requestID := httpserver.RequestIDFromContext(r.Context())
	if err := writePlaygroundEvent(w, map[string]any{"type": "phase", "phase": "submitted", "step": "请求已提交", "requestId": requestID}); err != nil {
		return
	}
	if err := controller.Flush(); err != nil {
		return
	}
	if input.Stream {
		a.streamPlayground(w, r, flusher, controller, command, requestID)
		return
	}
	a.completePlayground(w, r, flusher, command, requestID)
}

func (a *API) completePlayground(w http.ResponseWriter, r *http.Request, flusher http.Flusher, command requestflow.ChatCommand, requestID string) {
	result, workflowError := a.playground.Chat(r.Context(), command)
	if workflowError != nil {
		a.logPlaygroundWorkflowError(r, workflowError)
		_ = writePlaygroundEvent(w, map[string]any{"type": "error", "problem": playgroundProblem(workflowError, requestID)})
		flusher.Flush()
		return
	}
	for _, choice := range result.Response.Choices {
		if choice.Message.Reasoning != nil && choice.Message.Reasoning.Text != "" {
			_ = writePlaygroundEvent(w, map[string]any{"type": "reasoning", "delta": choice.Message.Reasoning.Text})
		}
		if content := canonicalText(choice.Message.Content); content != "" {
			_ = writePlaygroundEvent(w, map[string]any{"type": "content", "delta": content})
		}
		for _, call := range choice.Message.ToolCalls {
			_ = writePlaygroundEvent(w, map[string]any{"type": "tool_call", "name": call.Function.Name, "argumentsDelta": call.Function.Arguments})
		}
	}
	if result.Response.Usage != nil {
		_ = writePlaygroundUsage(w, *result.Response.Usage)
	}
	_ = writePlaygroundEvent(w, map[string]any{"type": "completed", "requestId": result.RequestID.String()})
	flusher.Flush()
}

func (a *API) streamPlayground(w http.ResponseWriter, r *http.Request, flusher http.Flusher, controller *http.ResponseController, command requestflow.ChatCommand, requestID string) {
	runningReported := false
	sink := func(logicalRequestID uuid.UUID, event canonical.StreamEvent) error {
		if !runningReported {
			if err := writePlaygroundEvent(w, map[string]any{"type": "phase", "phase": "running", "step": "上游正在响应", "requestId": logicalRequestID.String()}); err != nil {
				return err
			}
			if err := controller.Flush(); err != nil {
				return err
			}
			runningReported = true
		}
		var value map[string]any
		switch event.Type {
		case canonical.StreamContentDelta:
			value = map[string]any{"type": "content", "delta": event.ContentDelta}
		case canonical.StreamReasoningDelta:
			value = map[string]any{"type": "reasoning", "delta": event.ReasoningDelta}
		case canonical.StreamToolCallDelta:
			if event.ToolCallDelta == nil {
				return nil
			}
			value = map[string]any{"type": "tool_call", "name": event.ToolCallDelta.FunctionName, "argumentsDelta": event.ToolCallDelta.ArgumentsFragment}
		case canonical.StreamUsage:
			if event.Usage == nil {
				return nil
			}
			if err := writePlaygroundUsage(w, *event.Usage); err != nil {
				return err
			}
			return controller.Flush()
		case canonical.StreamDone:
			value = map[string]any{"type": "completed", "requestId": logicalRequestID.String()}
		default:
			return nil
		}
		if err := writePlaygroundEvent(w, value); err != nil {
			return err
		}
		return controller.Flush()
	}
	if workflowError := a.playground.Stream(r.Context(), command, sink); workflowError != nil {
		a.logPlaygroundWorkflowError(r, workflowError)
		_ = writePlaygroundEvent(w, map[string]any{"type": "error", "problem": playgroundProblem(workflowError, requestID)})
		flusher.Flush()
	}
}

func (a *API) logPlaygroundWorkflowError(r *http.Request, workflowError *canonical.Error) {
	if a.logger == nil || workflowError == nil {
		return
	}
	attributes := []any{
		"request_id", httpserver.RequestIDFromContext(r.Context()),
		"kind", workflowError.Kind,
		"code", workflowError.Code,
	}
	if workflowError.Cause != nil {
		attributes = append(attributes, "cause", workflowError.Cause)
	}
	a.logger.Error("playground workflow failed",
		attributes...,
	)
}

func (a *API) playgroundPrincipal(r *http.Request, keyID uuid.UUID) (identity.GatewayPrincipal, error) {
	items, err := a.collectKeys(r)
	if err != nil {
		return identity.GatewayPrincipal{}, err
	}
	now := time.Now().UTC()
	for _, item := range items {
		key := item.Key
		if key.ID != keyID {
			continue
		}
		if key.RevokedAt != nil || key.ExpiresAt != nil && !key.ExpiresAt.After(now) {
			return identity.GatewayPrincipal{}, identity.ErrInvalidCredential
		}
		return identity.GatewayPrincipal{KeyID: key.ID, UserID: key.UserID, Status: identity.StatusActive, KeyPrefix: key.Prefix, ExpiresAt: key.ExpiresAt}, nil
	}
	return identity.GatewayPrincipal{}, identity.ErrNotFound
}

func playgroundCommand(r *http.Request, principal identity.GatewayPrincipal, input playgroundRunInput, idempotencyKey string) (requestflow.ChatCommand, *canonical.Error) {
	wire := map[string]any{"model": input.Model, "stream": input.Stream, "messages": input.Messages}
	if input.ReasoningEnabled != nil {
		thinkingType := "disabled"
		if *input.ReasoningEnabled {
			thinkingType = "enabled"
		}
		wire["thinking"] = map[string]any{"type": thinkingType}
	}
	if input.ReasoningEffort != "" {
		wire["reasoning_effort"] = input.ReasoningEffort
	}
	if len(input.Tools) > 0 {
		tools := make([]map[string]any, 0, len(input.Tools))
		for _, tool := range input.Tools {
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": tool.Name, "description": tool.Description, "parameters": tool.Parameters}})
		}
		wire["tools"] = tools
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return requestflow.ChatCommand{}, &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "invalid_playground_request", Message: "Playground request could not be encoded.", Cause: err}
	}
	request, parseError := protocol.ParseChatRequest(bytes.NewReader(encoded), httpserver.RequestIDFromContext(r.Context()))
	if parseError != nil {
		return requestflow.ChatCommand{}, parseError
	}
	digest := sha256.Sum256(encoded)
	return requestflow.ChatCommand{Principal: principal, Request: request, RequestDigest: digest[:], IdempotencyKey: &idempotencyKey}, nil
}

func playgroundCapabilities(model requestflow.Model) []string {
	capabilities := make([]string, 0, 4)
	if model.Capabilities.Streaming {
		capabilities = append(capabilities, "streaming")
	}
	if model.Capabilities.Tools {
		capabilities = append(capabilities, "tools")
	}
	if model.Capabilities.Reasoning {
		capabilities = append(capabilities, "reasoning")
	}
	if model.Capabilities.StructuredOutput {
		capabilities = append(capabilities, "structured_output")
	}
	return capabilities
}

func writePlaygroundUsage(w http.ResponseWriter, usage canonical.Usage) error {
	if usage.InputTokens == nil || usage.OutputTokens == nil || usage.Source == canonical.UsageUnknown {
		return nil
	}
	return writePlaygroundEvent(w, map[string]any{"type": "usage", "inputTokens": *usage.InputTokens, "outputTokens": *usage.OutputTokens, "source": usage.Source})
}

func writePlaygroundEvent(w http.ResponseWriter, value any) error {
	return protocol.WriteSSE(w, value)
}

func playgroundProblem(providerError *canonical.Error, requestID string) problem {
	status := providerError.HTTPStatus
	if status < 400 || status > 599 {
		switch providerError.Kind {
		case canonical.ErrorInvalidRequest, canonical.ErrorUnsupportedCapability:
			status = http.StatusBadRequest
		case canonical.ErrorAuthentication:
			status = http.StatusUnauthorized
		case canonical.ErrorPermission:
			status = http.StatusForbidden
		case canonical.ErrorQuota:
			status = http.StatusPaymentRequired
		case canonical.ErrorAdmissionTimeout, canonical.ErrorRateLimit:
			status = http.StatusTooManyRequests
		case canonical.ErrorProviderTemporary, canonical.ErrorStorageUnavailable:
			status = http.StatusServiceUnavailable
		case canonical.ErrorProviderConfiguration, canonical.ErrorProviderPermanent, canonical.ErrorStreamInterrupted, canonical.ErrorUncertain:
			status = http.StatusBadGateway
		default:
			status = http.StatusInternalServerError
		}
	}
	retryable := providerError.Kind == canonical.ErrorProviderTemporary || providerError.Kind == canonical.ErrorStorageUnavailable || providerError.Kind == canonical.ErrorRateLimit
	if errors.Is(providerError, context.Canceled) {
		retryable = false
	}
	return problem{Status: status, Code: providerError.Code, Message: providerError.Message, Retryable: retryable, Stage: "playground", RequestID: requestID}
}

func canonicalText(parts []canonical.ContentPart) string {
	var builder strings.Builder
	for _, part := range parts {
		if part.Type == canonical.ContentPartText {
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}
