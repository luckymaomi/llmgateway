package providers

import (
	"context"
	"encoding/json"
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

func (a *openAIAdapter) ValidateProbe(kind ProbeKind, statusCode int, headers http.Header, body []byte) *canonical.Error {
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return a.ClassifyError(statusCode, headers, body)
	}
	if kind != ProbeModels {
		return a.requestError(canonical.ErrorProviderConfiguration, "unsupported_probe", "provider probe is not supported", "")
	}
	var envelope struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Data == nil {
		return a.requestError(canonical.ErrorProviderConfiguration, "invalid_probe_response", "provider returned an invalid models response", "")
	}
	return nil
}
