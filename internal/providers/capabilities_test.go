package providers

import (
	"context"
	"net/http"
	"testing"
)

func TestProviderCapabilitiesDescribeExecutableContracts(t *testing.T) {
	t.Parallel()

	zhipu := NewZhipu().Capabilities()
	if !zhipu.Chat || !zhipu.Models || !zhipu.Streaming || !zhipu.Tools || !zhipu.ToolStreaming || !zhipu.ReasoningReplay || !zhipu.ResponseRequestID {
		t.Fatalf("Zhipu capabilities = %#v", zhipu)
	}
	agnes := NewAgnes().Capabilities()
	if !agnes.Chat || !agnes.Models || !agnes.Streaming || !agnes.Tools || !agnes.ReasoningToggle {
		t.Fatalf("Agnes capabilities = %#v", agnes)
	}
	gemini := NewGemini().Capabilities()
	if !gemini.Chat || !gemini.Models || !gemini.Streaming || !gemini.Tools || !gemini.ToolStreaming || !gemini.ReasoningEffort || !gemini.StreamUsage {
		t.Fatalf("Gemini capabilities = %#v", gemini)
	}
}

func TestPublishedProviderModelsProbesAreNonGenerating(t *testing.T) {
	t.Parallel()

	compatible, err := NewOpenAICompatible(OpenAICompatibleOptions{
		BaseURL: "https://llm.example/v1", Capabilities: NarrowOpenAICompatibleCapabilities(),
	})
	if err != nil {
		t.Fatalf("create adapter: %v", err)
	}
	adapters := []struct {
		name     string
		adapter  Adapter
		endpoint string
	}{
		{name: "openai-compatible", adapter: compatible, endpoint: "https://llm.example/v1/models"},
		{name: "zhipu", adapter: NewZhipu(), endpoint: "https://open.bigmodel.cn/api/paas/v4/models"},
		{name: "agnes", adapter: NewAgnes(), endpoint: "https://apihub.agnes-ai.com/v1/models"},
		{name: "gemini", adapter: NewGemini(), endpoint: "https://generativelanguage.googleapis.com/v1beta/openai/models"},
	}
	for _, test := range adapters {
		probe, probeErr := test.adapter.Probe(context.Background(), Credential{APIKey: "fixture-key"})
		if probeErr != nil {
			t.Fatalf("%s build probe: %v", test.name, probeErr)
		}
		if !probe.Available || probe.MayConsumeTokens || probe.Kind != ProbeModels || probe.Request == nil {
			t.Fatalf("%s probe = %#v", test.name, probe)
		}
		if probe.Request.Method != http.MethodGet || probe.Request.URL.String() != test.endpoint {
			t.Fatalf("%s probe request = %s %s", test.name, probe.Request.Method, probe.Request.URL)
		}
	}
}
