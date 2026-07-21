package requestflow

import (
	"net/http"

	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/security"
)

type ProviderFactory struct {
	policy  security.SSRFPolicy
	catalog *providers.Catalog
}

func NewProviderFactory(policy security.SSRFPolicy) *ProviderFactory {
	return &ProviderFactory{policy: policy, catalog: providers.DefaultCatalog()}
}

func (f *ProviderFactory) Adapter(model Model) (providers.Adapter, error) {
	capabilities := providers.NarrowOpenAICompatibleCapabilities()
	capabilities.Streaming = model.Capabilities.Streaming
	capabilities.Tools = model.Capabilities.Tools
	capabilities.ToolStreaming = model.Capabilities.Tools && model.Capabilities.Streaming
	capabilities.ReasoningToggle = model.Capabilities.Reasoning
	capabilities.ReasoningEffort = model.Capabilities.Reasoning
	capabilities.ReasoningContent = model.Capabilities.Reasoning
	capabilities.ReasoningReplay = model.Capabilities.Reasoning
	capabilities.JSONOutput = model.Capabilities.StructuredOutput
	return f.catalog.Build(model.ProviderKind, providers.AdapterOptions{BaseURL: model.ProviderBaseURL, Capabilities: capabilities})
}

func (f *ProviderFactory) Client(candidate Candidate) (*http.Client, error) {
	client, err := security.NewSSRFSafeClient(f.policy)
	if err != nil {
		return nil, err
	}
	client.Timeout = 0
	return client, nil
}
