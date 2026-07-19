package requestflow

import (
	"fmt"
	"net/http"

	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/security"
)

type ProviderFactory struct {
	policy security.SSRFPolicy
}

func NewProviderFactory(policy security.SSRFPolicy) *ProviderFactory {
	return &ProviderFactory{policy: policy}
}

func (f *ProviderFactory) Adapter(model Model) (providers.Adapter, error) {
	switch model.ProviderKind {
	case providers.KindDeepSeek:
		return providers.NewDeepSeekWithBaseURL(model.ProviderBaseURL)
	case providers.KindZhipu:
		return providers.NewZhipuWithBaseURL(model.ProviderBaseURL)
	case providers.KindAgnes:
		return providers.NewAgnesWithBaseURL(model.ProviderBaseURL)
	case providers.KindOpenAICompatible:
		capabilities := providers.NarrowOpenAICompatibleCapabilities()
		capabilities.Streaming = model.Capabilities.Streaming
		capabilities.Tools = model.Capabilities.Tools
		capabilities.ToolStreaming = model.Capabilities.Tools && model.Capabilities.Streaming
		capabilities.ReasoningToggle = model.Capabilities.Reasoning
		capabilities.ReasoningEffort = model.Capabilities.Reasoning
		capabilities.ReasoningContent = model.Capabilities.Reasoning
		capabilities.ReasoningReplay = model.Capabilities.Reasoning
		capabilities.JSONOutput = model.Capabilities.StructuredOutput
		return providers.NewOpenAICompatible(providers.OpenAICompatibleOptions{BaseURL: model.ProviderBaseURL, Capabilities: capabilities})
	default:
		return nil, fmt.Errorf("unsupported provider kind %q", model.ProviderKind)
	}
}

func (f *ProviderFactory) Client(candidate Candidate) (*http.Client, error) {
	if candidate.FixedProxyURL != nil {
		return nil, fmt.Errorf("fixed proxy transport is not configured")
	}
	client, err := security.NewSSRFSafeClient(f.policy)
	if err != nil {
		return nil, err
	}
	client.Timeout = 0
	return client, nil
}
