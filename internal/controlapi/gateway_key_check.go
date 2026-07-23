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
	"github.com/luckymaomi/llmgateway/internal/requestflow"
)

type gatewayKeyTestModelView struct {
	ID    string `json:"id"`
	Alias string `json:"alias"`
}

type gatewayKeyTestRunInput struct {
	GatewayKeyID uuid.UUID `json:"apiKeyId"`
	Model        string    `json:"model"`
	Message      string    `json:"message"`
}

func (a *API) registerGatewayKeyTestRoutes(router chi.Router) {
	router.Get("/api-key-test/models", a.gatewayKeyTestModels)
	router.With(a.requireCSRF).Post("/api-key-test/runs", a.gatewayKeyTestRun)
}

func (a *API) gatewayKeyTestModels(w http.ResponseWriter, r *http.Request) {
	keyID, err := uuid.Parse(r.URL.Query().Get("apiKeyId"))
	if err != nil || keyID == uuid.Nil {
		writeProblem(w, r, problem{Status: http.StatusBadRequest, Code: "invalid_api_key", Message: "Select an active API key.", Stage: "api_key_test"})
		return
	}
	if _, err := a.gatewayKeyTestPrincipal(r, keyID); err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	models, err := a.gatewayKeyTest.Models(r.Context(), keyID)
	if err != nil {
		a.logFailure("API key test model catalog failed", r, err)
		writeProblem(w, r, problem{Status: http.StatusServiceUnavailable, Code: "model_catalog_unavailable", Message: "The selected API key model catalog is unavailable.", Retryable: true, Stage: "api_key_test"})
		return
	}
	views := make([]gatewayKeyTestModelView, 0, len(models))
	for _, model := range models {
		views = append(views, gatewayKeyTestModelView{ID: model.ID.String(), Alias: model.PublicName})
	}
	writeData(w, http.StatusOK, views)
}

func (a *API) gatewayKeyTestRun(w http.ResponseWriter, r *http.Request) {
	var input gatewayKeyTestRunInput
	if err := decodeJSON(w, r, &input); err != nil {
		writeDecodeError(w, r, err)
		return
	}
	principal, err := a.gatewayKeyTestPrincipal(r, input.GatewayKeyID)
	if err != nil {
		a.writeIdentityError(w, r, err)
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if _, err := uuid.Parse(idempotencyKey); err != nil {
		writeProblem(w, r, problem{Status: http.StatusBadRequest, Code: "invalid_idempotency_key", Message: "Idempotency-Key must be a UUID.", Stage: "api_key_test"})
		return
	}
	command, parseError := gatewayKeyTestCommand(r, principal, input, idempotencyKey)
	if parseError != nil {
		writeProblem(w, r, gatewayKeyTestProblem(parseError, httpserver.RequestIDFromContext(r.Context())))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeProblem(w, r, problem{Status: http.StatusInternalServerError, Code: "streaming_unavailable", Message: "HTTP streaming is unavailable.", Stage: "api_key_test"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	requestID := httpserver.RequestIDFromContext(r.Context())
	if err := writeGatewayKeyTestEvent(w, map[string]any{"type": "phase", "phase": "submitted", "step": "请求已提交", "requestId": requestID}); err != nil {
		return
	}
	if err := controller.Flush(); err != nil {
		return
	}
	a.streamGatewayKeyTest(w, r, flusher, controller, command, requestID)
}

func (a *API) streamGatewayKeyTest(w http.ResponseWriter, r *http.Request, flusher http.Flusher, controller *http.ResponseController, command requestflow.ChatCommand, requestID string) {
	runningReported := false
	sink := func(logicalRequestID uuid.UUID, event canonical.StreamEvent) error {
		if !runningReported {
			if err := writeGatewayKeyTestEvent(w, map[string]any{"type": "phase", "phase": "running", "step": "上游正在响应", "requestId": logicalRequestID.String()}); err != nil {
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
		case canonical.StreamUsage:
			if event.Usage == nil {
				return nil
			}
			if err := writeGatewayKeyTestUsage(w, *event.Usage); err != nil {
				return err
			}
			return controller.Flush()
		case canonical.StreamDone:
			value = map[string]any{"type": "completed", "requestId": logicalRequestID.String()}
		default:
			return nil
		}
		if err := writeGatewayKeyTestEvent(w, value); err != nil {
			return err
		}
		return controller.Flush()
	}
	if workflowError := a.gatewayKeyTest.Stream(r.Context(), command, sink); workflowError != nil {
		a.logGatewayKeyTestWorkflowError(r, workflowError)
		_ = writeGatewayKeyTestEvent(w, map[string]any{"type": "error", "problem": gatewayKeyTestProblem(workflowError, requestID)})
		flusher.Flush()
	}
}

func (a *API) logGatewayKeyTestWorkflowError(r *http.Request, workflowError *canonical.Error) {
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
	a.logger.Error("API key test workflow failed",
		attributes...,
	)
}

func (a *API) gatewayKeyTestPrincipal(r *http.Request, keyID uuid.UUID) (identity.GatewayPrincipal, error) {
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

func gatewayKeyTestCommand(r *http.Request, principal identity.GatewayPrincipal, input gatewayKeyTestRunInput, idempotencyKey string) (requestflow.ChatCommand, *canonical.Error) {
	wire := map[string]any{
		"model":    strings.TrimSpace(input.Model),
		"stream":   true,
		"messages": []map[string]string{{"role": "user", "content": strings.TrimSpace(input.Message)}},
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		return requestflow.ChatCommand{}, &canonical.Error{Kind: canonical.ErrorInvalidRequest, Code: "invalid_api_key_test_request", Message: "API key test request could not be encoded.", Cause: err}
	}
	request, parseError := protocol.ParseChatRequest(bytes.NewReader(encoded), httpserver.RequestIDFromContext(r.Context()))
	if parseError != nil {
		return requestflow.ChatCommand{}, parseError
	}
	digest := sha256.Sum256(encoded)
	return requestflow.ChatCommand{Principal: principal, Request: request, RequestDigest: digest[:], IdempotencyKey: &idempotencyKey}, nil
}

func writeGatewayKeyTestUsage(w http.ResponseWriter, usage canonical.Usage) error {
	if usage.InputTokens == nil || usage.OutputTokens == nil || usage.Source == canonical.UsageUnknown {
		return nil
	}
	return writeGatewayKeyTestEvent(w, map[string]any{"type": "usage", "inputTokens": *usage.InputTokens, "outputTokens": *usage.OutputTokens, "source": usage.Source})
}

func writeGatewayKeyTestEvent(w http.ResponseWriter, value any) error {
	return protocol.WriteSSE(w, value)
}

func gatewayKeyTestProblem(providerError *canonical.Error, requestID string) problem {
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
	return problem{Status: status, Code: providerError.Code, Message: providerError.Message, Retryable: retryable, Stage: "api_key_test", RequestID: requestID}
}
