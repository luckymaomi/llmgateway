package providers

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/luckymaomi/llmgateway/internal/canonical"
)

func TestZhipuBuildsPreservedToolStreamRequest(t *testing.T) {
	t.Parallel()

	adapter := NewZhipu()
	request, err := adapter.BuildRequest(context.Background(), Credential{APIKey: "fixture-key"}, canonical.ChatRequest{
		RequestID: "gateway-request-7",
		Model:     "glm-5.2",
		Messages:  []canonical.Message{{Role: canonical.RoleUser, Content: canonical.TextContent("Check weather")}},
		Tools: []canonical.ToolDefinition{{
			Name: "get_weather", Parameters: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
		}},
		ToolChoice: &canonical.ToolChoice{Mode: canonical.ToolChoiceAuto},
		Stream:     true,
		Reasoning: &canonical.ReasoningConfig{
			Enabled: boolPointer(true), Effort: canonical.ReasoningEffortMax, Preserve: boolPointer(true),
		},
	})
	if err != nil {
		t.Fatalf("build Zhipu request: %v", err)
	}
	if request.URL.String() != "https://open.bigmodel.cn/api/paas/v4/chat/completions" {
		t.Fatalf("request URL = %q", request.URL.String())
	}
	assertRequestJSON(t, request, `{
		"model":"glm-5.2",
		"messages":[{"role":"user","content":"Check weather"}],
		"stream":true,
		"tools":[{"type":"function","function":{"name":"get_weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}],
		"tool_choice":"auto",
		"thinking":{"type":"enabled","clear_thinking":false},
		"reasoning_effort":"max",
		"tool_stream":true,
		"request_id":"gateway-request-7"
	}`)
}

func TestAgnesBuildsThinkingToolRequest(t *testing.T) {
	t.Parallel()

	adapter := NewAgnes()
	request, err := adapter.BuildRequest(context.Background(), Credential{APIKey: "fixture-key"}, canonical.ChatRequest{
		Model:    "agnes-2.0-flash",
		Messages: []canonical.Message{{Role: canonical.RoleUser, Content: canonical.TextContent("Find the build status")}},
		Tools: []canonical.ToolDefinition{{
			Name: "get_build", Parameters: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}}}`),
		}},
		Stream:    true,
		Reasoning: &canonical.ReasoningConfig{Enabled: boolPointer(true)},
	})
	if err != nil {
		t.Fatalf("build Agnes request: %v", err)
	}
	if request.URL.String() != "https://apihub.agnes-ai.com/v1/chat/completions" {
		t.Fatalf("request URL = %q", request.URL.String())
	}
	assertRequestJSON(t, request, `{
		"model":"agnes-2.0-flash",
		"messages":[{"role":"user","content":"Find the build status"}],
		"stream":true,
		"tools":[{"type":"function","function":{"name":"get_build","parameters":{"type":"object","properties":{"id":{"type":"string"}}}}}],
		"chat_template_kwargs":{"enable_thinking":true}
	}`)
}

func TestOpenAICompatibleBuildsDeclaredContractRequest(t *testing.T) {
	t.Parallel()

	adapter, err := NewOpenAICompatible(OpenAICompatibleOptions{
		BaseURL: "https://llm.example/v1", Capabilities: NarrowOpenAICompatibleCapabilities(),
	})
	if err != nil {
		t.Fatalf("create adapter: %v", err)
	}
	request, err := adapter.BuildRequest(context.Background(), Credential{APIKey: "fixture-key"}, canonical.ChatRequest{
		Model:    "general-chat",
		Messages: []canonical.Message{{Role: canonical.RoleUser, Name: "caller", Content: canonical.TextContent("Summarize")}},
		Stream:   true,
		Reasoning: &canonical.ReasoningConfig{
			Effort: canonical.ReasoningEffortMedium,
		},
	})
	if err != nil {
		t.Fatalf("build compatible request: %v", err)
	}
	assertRequestJSON(t, request, `{
		"model":"general-chat",
		"messages":[{"role":"user","name":"caller","content":"Summarize"}],
		"stream":true,
		"stream_options":{"include_usage":true},
		"reasoning_effort":"medium"
	}`)
}

func TestOpenAICompatibleRejectsBaseURLParameters(t *testing.T) {
	t.Parallel()

	for _, baseURL := range []string{
		"https://llm.example/v1?",
		"https://llm.example/v1?tenant=other",
		"https://llm.example/v1#other",
	} {
		t.Run(baseURL, func(t *testing.T) {
			t.Parallel()
			if _, err := NewOpenAICompatible(OpenAICompatibleOptions{
				BaseURL: baseURL, Capabilities: NarrowOpenAICompatibleCapabilities(),
			}); err == nil {
				t.Fatalf("NewOpenAICompatible(%q) succeeded", baseURL)
			}
		})
	}
}
