package providers

import (
	"context"
	"net/http"
	"strings"

	"github.com/luckymaomi/llmgateway/internal/canonical"
)

func (a *openAIAdapter) Probe(ctx context.Context, credential Credential) (Probe, error) {
	if !a.policy.capabilities.Models || a.policy.modelsPath == "" {
		return Probe{}, nil
	}
	if strings.TrimSpace(credential.APIKey) == "" {
		return Probe{}, a.requestError(canonical.ErrorProviderConfiguration, "missing_api_key", "provider API key is required", "")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, a.endpoint(a.policy.modelsPath), nil)
	if err != nil {
		return Probe{}, a.requestError(canonical.ErrorProviderConfiguration, "build_probe", "could not build provider probe", "")
	}
	request.Header.Set("Authorization", "Bearer "+credential.APIKey)
	request.Header.Set("Accept", "application/json")
	return Probe{Available: true, MayConsumeTokens: false, Kind: ProbeModels, Request: request}, nil
}
