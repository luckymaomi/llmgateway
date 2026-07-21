package requestflow

import (
	"net/http"
	"sync"

	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/registry"
	"github.com/luckymaomi/llmgateway/internal/security"
)

type ProviderFactory struct {
	policy     security.SSRFPolicy
	catalog    *providers.Catalog
	clientOnce sync.Once
	client     *http.Client
	clientErr  error
}

func NewProviderFactory(policy security.SSRFPolicy) *ProviderFactory {
	return &ProviderFactory{policy: policy, catalog: providers.DefaultCatalog()}
}

func (f *ProviderFactory) Adapter(model Model) (providers.Adapter, error) {
	capabilities := providers.NarrowOpenAICompatibleCapabilities()
	capabilities.Streaming = model.Capabilities.Streaming
	capabilities.Tools = model.Capabilities.Tools
	capabilities.ToolStreaming = model.Capabilities.Tools && model.Capabilities.Streaming
	capabilities.ReasoningToggle = model.Capabilities.ReasoningMode == registry.ReasoningToggle || model.Capabilities.ReasoningMode == registry.ReasoningHybrid
	capabilities.ReasoningEffort = model.Capabilities.ReasoningMode == registry.ReasoningEffort || model.Capabilities.ReasoningMode == registry.ReasoningHybrid
	capabilities.ReasoningContent = model.Capabilities.Reasoning
	capabilities.ReasoningReplay = false
	capabilities.JSONOutput = model.Capabilities.StructuredOutput
	return f.catalog.Build(model.ProviderKind, providers.AdapterOptions{BaseURL: model.ProviderBaseURL, Capabilities: capabilities})
}

func (f *ProviderFactory) Client(candidate Candidate) (*http.Client, error) {
	f.clientOnce.Do(func() {
		f.client, f.clientErr = security.NewSSRFSafeClient(f.policy)
		if f.client != nil {
			f.client.Timeout = 0
		}
	})
	return f.client, f.clientErr
}
