package protocol

import (
	"strings"
	"testing"

	"github.com/luckymaomi/llmgateway/internal/canonical"
)

func TestParseChatRequestPreservesToolAndReasoningRoundTrip(t *testing.T) {
	body := `{
  "model":"deepseek-chat",
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
	if request.Model != "deepseek-chat" || !request.Stream || len(request.Tools) != 1 || request.Reasoning == nil || request.Reasoning.Effort != canonical.ReasoningEffortHigh || request.Reasoning.Preserve == nil || !*request.Reasoning.Preserve {
		t.Fatalf("parsed request lost a canonical contract: %#v", request)
	}
	if len(request.Messages[0].ToolCalls) != 1 || request.Messages[1].ToolCallID != "call_1" {
		t.Fatal("tool round trip was not preserved")
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
