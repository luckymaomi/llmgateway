package providers

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/luckymaomi/llmgateway/internal/canonical"
)

func TestProviderResponsesBecomeCanonicalFacts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		adapter       Adapter
		body          string
		wantRequestID string
		wantContent   string
		wantReasoning string
		wantTool      string
		wantInput     int64
	}{
		{
			name:    "Zhipu request identity and reasoning",
			adapter: NewZhipu(),
			body: `{
				"id":"chat-glm-1","request_id":"upstream-request-9","created":1710000001,"model":"glm-5.2",
				"choices":[{"index":0,"message":{"role":"assistant","content":"Build passed.","reasoning_content":"I checked the build result."},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":13,"completion_tokens":5,"total_tokens":18,"prompt_tokens_details":{"cached_tokens":3}}
			}`,
			wantRequestID: "upstream-request-9", wantContent: "Build passed.", wantReasoning: "I checked the build result.", wantInput: 13,
		},
		{
			name:    "Agnes chat content",
			adapter: NewAgnes(),
			body: `{
				"id":"chat-agnes-1","created":1710000002,"model":"agnes-2.0-flash",
				"choices":[{"index":0,"message":{"role":"assistant","content":"Deployment is healthy."},"finish_reason":"stop"}]
			}`,
			wantContent: "Deployment is healthy.",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			response, err := test.adapter.ParseResponse(http.StatusOK, nil, []byte(test.body))
			if err != nil {
				t.Fatalf("parse response: %v", err)
			}
			if response.RequestID != test.wantRequestID {
				t.Fatalf("request ID = %q, want %q", response.RequestID, test.wantRequestID)
			}
			if len(response.Choices) != 1 || len(response.Choices[0].Message.Content) != 1 {
				t.Fatalf("choices = %#v", response.Choices)
			}
			if got := response.Choices[0].Message.Content[0].Text; got != test.wantContent {
				t.Fatalf("content = %q, want %q", got, test.wantContent)
			}
			if test.wantReasoning != "" {
				if response.Choices[0].Message.Reasoning == nil || response.Choices[0].Message.Reasoning.Text != test.wantReasoning {
					t.Fatalf("reasoning = %#v", response.Choices[0].Message.Reasoning)
				}
			}
			if test.wantInput > 0 {
				if response.Usage == nil || response.Usage.InputTokens == nil || *response.Usage.InputTokens != test.wantInput || response.Usage.Source != canonical.UsageAuthoritative {
					t.Fatalf("usage = %#v", response.Usage)
				}
			}
		})
	}
}

func TestOpenAICompatibleParsesToolResponseAndDeclaredRequestID(t *testing.T) {
	t.Parallel()

	adapter, err := NewOpenAICompatible(OpenAICompatibleOptions{
		BaseURL: "https://llm.example/v1", Capabilities: NarrowOpenAICompatibleCapabilities(), RequestIDHeader: "X-Request-ID",
	})
	if err != nil {
		t.Fatalf("create adapter: %v", err)
	}
	headers := http.Header{"X-Request-Id": []string{"provider-request-4"}}
	response, err := adapter.ParseResponse(http.StatusOK, headers, []byte(`{
		"id":"chat-tool-1","created":1710000003,"model":"general-chat",
		"choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"id\":\"42\"}"}}]},"finish_reason":"tool_calls"}],
		"usage":{"prompt_tokens":10,"completion_tokens":6,"total_tokens":16}
	}`))
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if response.RequestID != "provider-request-4" {
		t.Fatalf("request ID = %q", response.RequestID)
	}
	toolCalls := response.Choices[0].Message.ToolCalls
	if len(toolCalls) != 1 || toolCalls[0].Function.Name != "lookup" || toolCalls[0].Function.Arguments != `{"id":"42"}` {
		t.Fatalf("tool calls = %#v", toolCalls)
	}
	if response.CreatedAt != time.Unix(1710000003, 0).UTC() {
		t.Fatalf("created at = %s", response.CreatedAt)
	}
}

func TestOpenAICompatibleStreamReassemblesReasoningToolsAndUsage(t *testing.T) {
	t.Parallel()
	capabilities := NarrowOpenAICompatibleCapabilities()
	capabilities.Streaming = true
	capabilities.Tools = true
	capabilities.ToolStreaming = true
	capabilities.ReasoningContent = true
	capabilities.ResponseUsage = true
	capabilities.StreamUsage = true
	adapter, err := NewOpenAICompatible(OpenAICompatibleOptions{
		BaseURL: "https://llm.example/v1", Capabilities: capabilities,
	})
	if err != nil {
		t.Fatalf("create adapter: %v", err)
	}

	input := []byte(
		"data: {\"id\":\"stream-1\",\"created\":1710000100,\"model\":\"reasoning-chat\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"reasoning_content\":\"Need weather.\"},\"finish_reason\":null}]}\r\n\r\n" +
			"data: {\"id\":\"stream-1\",\"created\":1710000100,\"model\":\"reasoning-chat\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_weather\",\"type\":\"function\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"{\\\"city\\\":\"}}]},\"finish_reason\":null}]}\n\n" +
			"data: {\"id\":\"stream-1\",\"created\":1710000100,\"model\":\"reasoning-chat\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"Hangzhou\\\"}\"}}]},\"finish_reason\":null}]}\n\n" +
			"data: {\"id\":\"stream-1\",\"created\":1710000100,\"model\":\"reasoning-chat\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":20,\"completion_tokens\":8,\"total_tokens\":28}}\n\n" +
			"data: [DONE]\n\n")

	parser := adapter.ParseStream()
	var events []canonical.StreamEvent
	pattern := []int{1, 7, 2, 13, 3, 5}
	for offset, patternIndex := 0, 0; offset < len(input); patternIndex++ {
		end := offset + pattern[patternIndex%len(pattern)]
		if end > len(input) {
			end = len(input)
		}
		parsed, err := parser.Feed(input[offset:end])
		if err != nil {
			t.Fatalf("feed stream at byte %d: %v", offset, err)
		}
		events = append(events, parsed...)
		offset = end
	}
	parsed, err := parser.Close()
	if err != nil {
		t.Fatalf("close stream: %v", err)
	}
	events = append(events, parsed...)

	wantTypes := []canonical.StreamEventType{
		canonical.StreamMessageStart,
		canonical.StreamReasoningDelta,
		canonical.StreamToolCallDelta,
		canonical.StreamToolCallDelta,
		canonical.StreamFinish,
		canonical.StreamUsage,
		canonical.StreamDone,
	}
	if len(events) != len(wantTypes) {
		t.Fatalf("events = %#v", events)
	}
	for index, wantType := range wantTypes {
		if events[index].Type != wantType {
			t.Fatalf("event %d type = %q, want %q", index, events[index].Type, wantType)
		}
		if events[index].CompletionID != "stream-1" || events[index].Model != "reasoning-chat" {
			t.Fatalf("event %d metadata = %#v", index, events[index])
		}
		if events[index].CreatedAt != time.Unix(1710000100, 0).UTC() {
			t.Fatalf("event %d created at = %s", index, events[index].CreatedAt)
		}
	}
	if events[1].ReasoningDelta != "Need weather." {
		t.Fatalf("reasoning delta = %q", events[1].ReasoningDelta)
	}
	if events[2].ToolCallDelta.FunctionName != "get_weather" || events[2].ToolCallDelta.ArgumentsFragment != `{"city":` {
		t.Fatalf("first tool delta = %#v", events[2].ToolCallDelta)
	}
	if events[3].ToolCallDelta.ArgumentsFragment != `"Hangzhou"}` {
		t.Fatalf("second tool delta = %#v", events[3].ToolCallDelta)
	}
	if events[5].Usage == nil || events[5].Usage.TotalTokens == nil || *events[5].Usage.TotalTokens != 28 {
		t.Fatalf("usage event = %#v", events[5])
	}
}

func TestStreamEOFProducesInterruptedFact(t *testing.T) {
	t.Parallel()

	parser := NewAgnes().ParseStream()
	events, err := parser.Feed([]byte("data: {\"id\":\"partial-1\",\"created\":1710000101,\"model\":\"agnes-2.0-flash\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"partial\"},\"finish_reason\":null}]}\n\n"))
	if err != nil {
		t.Fatalf("feed partial stream: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("partial events = %#v", events)
	}
	ending, err := parser.Close()
	if err != nil {
		t.Fatalf("close partial stream: %v", err)
	}
	if len(ending) != 1 || ending[0].Type != canonical.StreamError || ending[0].Error == nil || ending[0].Error.Kind != canonical.ErrorStreamInterrupted {
		t.Fatalf("ending events = %#v", ending)
	}
}

func TestZhipuStreamCarriesBodyRequestID(t *testing.T) {
	t.Parallel()

	parser := NewZhipu().ParseStream()
	events, err := parser.Feed([]byte("data: {\"id\":\"glm-stream-1\",\"request_id\":\"zhipu-request-1\",\"created\":1710000102,\"model\":\"glm-5.2\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Ready\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	if err != nil {
		t.Fatalf("parse Zhipu stream: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("events = %#v", events)
	}
	for index, event := range events {
		if event.RequestID != "zhipu-request-1" {
			t.Fatalf("event %d request ID = %q", index, event.RequestID)
		}
	}
	if _, err := parser.Close(); err != nil {
		t.Fatalf("close Zhipu stream: %v", err)
	}
}

func TestExplicitStreamErrorIsTerminalFailureFact(t *testing.T) {
	t.Parallel()

	parser := NewAgnes().ParseStream()
	events, err := parser.Feed([]byte("data: {\"error\":{\"message\":\"overloaded\",\"code\":\"upstream_overloaded\"}}\n\n"))
	if err != nil {
		t.Fatalf("parse error stream: %v", err)
	}
	if len(events) != 1 || events[0].Type != canonical.StreamError || events[0].Error == nil || events[0].Error.Kind != canonical.ErrorProviderPermanent {
		t.Fatalf("error events = %#v", events)
	}
	ending, err := parser.Close()
	if err != nil {
		t.Fatalf("close error stream: %v", err)
	}
	if len(ending) != 0 {
		t.Fatalf("terminal events = %#v", ending)
	}
}

func TestMalformedSuccessJSONReturnsProviderContractError(t *testing.T) {
	t.Parallel()

	_, err := NewAgnes().ParseResponse(http.StatusOK, nil, []byte(`{"id":"one"}{"id":"two"}`))
	var providerError *canonical.Error
	if !errors.As(err, &providerError) || providerError.Kind != canonical.ErrorProviderPermanent || providerError.Code != "malformed_response" {
		t.Fatalf("error = %#v", err)
	}
}
