package publicapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/canonical"
	"github.com/luckymaomi/llmgateway/internal/identity"
	"github.com/luckymaomi/llmgateway/internal/requestflow"
)

type fakeIdentity struct {
	principal identity.GatewayPrincipal
	err       error
}

func (f fakeIdentity) AuthenticateGatewayKey(context.Context, string) (identity.GatewayPrincipal, error) {
	return f.principal, f.err
}

func (f fakeIdentity) GatewayPrincipalByID(context.Context, uuid.UUID) (identity.GatewayPrincipal, error) {
	return f.principal, f.err
}

type fakeWorkflow struct {
	models          []requestflow.Model
	chatResult      requestflow.ChatResult
	chatError       *canonical.Error
	streamRequestID uuid.UUID
	streamEvents    []canonical.StreamEvent
	streamError     *canonical.Error
}

func (f fakeWorkflow) Models(context.Context, uuid.UUID) ([]requestflow.Model, error) {
	return f.models, nil
}

func (f fakeWorkflow) Chat(context.Context, requestflow.ChatCommand) (requestflow.ChatResult, *canonical.Error) {
	return f.chatResult, f.chatError
}

func (f fakeWorkflow) Stream(_ context.Context, _ requestflow.ChatCommand, sink requestflow.StreamSink) *canonical.Error {
	requestID := f.streamRequestID
	if requestID == uuid.Nil {
		requestID = uuid.New()
	}
	for _, event := range f.streamEvents {
		if err := sink(requestID, event); err != nil {
			return &canonical.Error{Kind: canonical.ErrorStreamInterrupted, Code: "sink", Message: err.Error()}
		}
	}
	return f.streamError
}

func TestModelsRequireGatewayKeyAndExposeOnlyWorkflowCatalog(t *testing.T) {
	userID := uuid.New()
	api := New(fakeIdentity{principal: identity.GatewayPrincipal{UserID: userID}}, fakeWorkflow{models: []requestflow.Model{{PublicName: "glm-free", ProviderSlug: "zhipu", CreatedAt: time.Unix(42, 0)}}}, testLogger())

	unauthorized := httptest.NewRecorder()
	api.Routes().ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/models", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "/models", nil)
	request.Header.Set("Authorization", "Bearer llmg_test")
	response := httptest.NewRecorder()
	api.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"glm-free"`) || !strings.Contains(response.Body.String(), `"owned_by":"zhipu"`) {
		t.Fatalf("unexpected models response: %d %s", response.Code, response.Body.String())
	}
}

func TestChatCompletionPresentsCanonicalResponse(t *testing.T) {
	completionID := "chatcmpl_test"
	api := New(fakeIdentity{principal: identity.GatewayPrincipal{UserID: uuid.New(), KeyID: uuid.New()}}, fakeWorkflow{chatResult: requestflow.ChatResult{
		RequestID: uuid.New(), Response: canonical.ChatResponse{ID: completionID, Model: "glm-free", CreatedAt: time.Unix(50, 0), Choices: []canonical.ChatChoice{{Index: 0, Message: canonical.Message{Role: canonical.RoleAssistant, Content: canonical.TextContent("hello")}, FinishReason: canonical.FinishReasonStop}}},
	}}, testLogger())
	request := httptest.NewRequest(http.MethodPost, "/chat/completions", strings.NewReader(`{"model":"glm-free","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Authorization", "Bearer llmg_test")
	response := httptest.NewRecorder()
	api.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), completionID) || !strings.Contains(response.Body.String(), `"content":"hello"`) {
		t.Fatalf("unexpected chat response: %d %s", response.Code, response.Body.String())
	}
}

func TestStreamCommitsOnlyWhenWorkflowEmits(t *testing.T) {
	streamRequestID := uuid.New()
	api := New(fakeIdentity{principal: identity.GatewayPrincipal{UserID: uuid.New(), KeyID: uuid.New()}}, fakeWorkflow{streamRequestID: streamRequestID, streamEvents: []canonical.StreamEvent{
		{Type: canonical.StreamMessageStart, CompletionID: "chatcmpl_stream", Model: "glm-free", Role: canonical.RoleAssistant},
		{Type: canonical.StreamContentDelta, CompletionID: "chatcmpl_stream", Model: "glm-free", ContentDelta: "hello"},
		{Type: canonical.StreamDone, CompletionID: "chatcmpl_stream", Model: "glm-free"},
	}}, testLogger())
	request := httptest.NewRequest(http.MethodPost, "/chat/completions", strings.NewReader(`{"model":"glm-free","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Authorization", "Bearer llmg_test")
	response := httptest.NewRecorder()
	api.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/event-stream" || response.Header().Get("X-Gateway-Request-ID") != streamRequestID.String() || !strings.Contains(response.Body.String(), "data: [DONE]") {
		t.Fatalf("unexpected stream response: %d %s", response.Code, response.Body.String())
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
