package protocol

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/luckymaomi/llmgateway/internal/canonical"
)

func TestParseChatRequestPreservesToolAndReasoningRoundTrip(t *testing.T) {
	body := `{
  "model":"reasoning-chat",
  "messages":[
    {"role":"assistant","content":null,"reasoning_content":"checked","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"id\":1}"}}]},
    {"role":"tool","tool_call_id":"call_1","content":"found"}
  ],
  "tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"id":{"type":"integer"}}}}}],
  "tool_choice":"auto",
  "stream":true,
  "thinking":{"type":"enabled","clear_thinking":false},
  "reasoning_effort":"high"
}`
	request, parseError := ParseChatRequest(strings.NewReader(body), "req_1")
	if parseError != nil {
		t.Fatal(parseError)
	}
	if request.Model != "reasoning-chat" || !request.Stream || len(request.Tools) != 1 || request.Reasoning == nil || request.Reasoning.Effort != canonical.ReasoningEffortHigh || request.Reasoning.Preserve == nil || !*request.Reasoning.Preserve {
		t.Fatalf("parsed request lost a canonical contract: %#v", request)
	}
	if len(request.Messages[0].ToolCalls) != 1 || request.Messages[1].ToolCallID != "call_1" {
		t.Fatal("tool round trip was not preserved")
	}
}

func TestUncertainErrorPreventsImplicitSDKReplay(t *testing.T) {
	retryAt := time.Date(2026, 7, 21, 16, 0, 0, 0, time.UTC)
	recorder := httptest.NewRecorder()
	WriteError(recorder, "request_1", &canonical.Error{
		Kind: canonical.ErrorUncertain, Code: "upstream_outcome_uncertain", Message: "upstream request outcome is uncertain",
		RetryAfter: &canonical.RetryAfter{At: &retryAt},
	})
	if recorder.Code != http.StatusConflict || recorder.Header().Get("Retry-After") != retryAt.Format(http.TimeFormat) {
		t.Fatalf("status/retry-after = %d/%q", recorder.Code, recorder.Header().Get("Retry-After"))
	}
}

func TestChatToolCallPreservesProviderMetadata(t *testing.T) {
	body := `{"model":"gemini","messages":[{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"},"extra_content":{"google":{"thought_signature":"signed-thought"}}}]}]}`
	request, parseError := ParseChatRequest(strings.NewReader(body), "req_1")
	if parseError != nil {
		t.Fatal(parseError)
	}
	metadata := request.Messages[0].ToolCalls[0].ProviderMetadata
	if metadata == nil || metadata.GoogleThoughtSignature != "signed-thought" {
		t.Fatalf("tool metadata = %#v", metadata)
	}
	presented := PresentChatResponse(canonical.ChatResponse{
		ID: "chat_1", Model: "gemini", Choices: []canonical.ChatChoice{{Index: 0, Message: request.Messages[0], FinishReason: canonical.FinishReasonToolCalls}},
	})
	choices := presented["choices"].([]map[string]any)
	message := choices[0]["message"].(map[string]any)
	calls := message["tool_calls"].([]map[string]any)
	extra := calls[0]["extra_content"].(map[string]any)
	google := extra["google"].(map[string]any)
	if google["thought_signature"] != "signed-thought" {
		t.Fatalf("presented metadata = %#v", extra)
	}
}

func TestPresentStreamEventProducesOpenAIChunk(t *testing.T) {
	event := canonical.StreamEvent{Type: canonical.StreamToolCallDelta, CompletionID: "chatcmpl_1", Model: "model", ChoiceIndex: 0, ToolCallDelta: &canonical.ToolCallDelta{Index: 0, ID: "call_1", Type: "function", FunctionName: "lookup", ArgumentsFragment: "{\"id\":"}}
	chunk := PresentStreamEvent(event)
	choices, ok := chunk["choices"].([]map[string]any)
	if !ok || len(choices) != 1 {
		t.Fatalf("stream chunk choices = %#v", chunk["choices"])
	}
	delta := choices[0]["delta"].(map[string]any)
	toolCalls := delta["tool_calls"].([]map[string]any)
	if toolCalls[0]["id"] != "call_1" {
		t.Fatalf("tool call delta = %#v", toolCalls[0])
	}
}
