package providers

import (
	"context"
	"net/http"
	"testing"
)

func TestProviderCapabilitiesDescribeExecutableContracts(t *testing.T) {
	t.Parallel()

	zhipu := NewZhipu().Capabilities()
	if !zhipu.Chat || !zhipu.Streaming || !zhipu.Tools || !zhipu.ToolStreaming || !zhipu.ReasoningReplay || !zhipu.ResponseRequestID {
		t.Fatalf("Zhipu capabilities = %#v", zhipu)
	}
	agnes := NewAgnes().Capabilities()
	if !agnes.Chat || !agnes.Streaming || !agnes.Tools || !agnes.ReasoningToggle {
		t.Fatalf("Agnes capabilities = %#v", agnes)
	}
}

func TestOpenAICompatibleModelsProbeIsNonGenerating(t *testing.T) {
	t.Parallel()

	adapter, err := NewOpenAICompatible(OpenAICompatibleOptions{
		BaseURL: "https://llm.example/v1", Capabilities: NarrowOpenAICompatibleCapabilities(),
	})
	if err != nil {
		t.Fatalf("create adapter: %v", err)
	}
	probe, err := adapter.Probe(context.Background(), Credential{APIKey: "fixture-key"})
	if err != nil {
		t.Fatalf("build probe: %v", err)
	}
	if !probe.Available || probe.MayConsumeTokens || probe.Kind != ProbeModels || probe.Request == nil {
		t.Fatalf("probe = %#v", probe)
	}
	if probe.Request.Method != http.MethodGet || probe.Request.URL.String() != "https://llm.example/v1/models" {
		t.Fatalf("probe request = %s %s", probe.Request.Method, probe.Request.URL)
	}
}
