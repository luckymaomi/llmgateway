package credentialprobe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/luckymaomi/llmgateway/internal/canonical"
	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/registry"
	"github.com/luckymaomi/llmgateway/internal/security"
)

type Executor struct {
	policy           security.SSRFPolicy
	timeout          time.Duration
	maxResponseBytes int64
	catalog          *providers.Catalog
}

func New(policy security.SSRFPolicy, timeout time.Duration, maxResponseBytes int64) (*Executor, error) {
	if timeout <= 0 || maxResponseBytes < 1024 {
		return nil, errors.New("credential probe bounds are invalid")
	}
	if _, err := security.NewURLValidator(policy); err != nil {
		return nil, fmt.Errorf("credential probe SSRF policy: %w", err)
	}
	return &Executor{policy: policy, timeout: timeout, maxResponseBytes: maxResponseBytes, catalog: providers.DefaultCatalog()}, nil
}

func (e *Executor) Execute(ctx context.Context, target registry.CredentialProbeTarget) registry.CredentialProbeExecution {
	startedAt := time.Now()
	result := registry.CredentialProbeExecution{Kind: string(providers.ProbeModels), Status: "unavailable"}
	adapter, err := e.catalog.Build(target.Provider.Kind, providers.AdapterOptions{
		BaseURL: target.Provider.BaseURL, Capabilities: providers.NarrowOpenAICompatibleCapabilities(),
	})
	if err != nil {
		result.Status = "failed"
		result.ErrorKind = stringPointer(string(canonical.ErrorProviderConfiguration))
		return withLatency(result, startedAt)
	}
	probeContext, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	probe, err := adapter.Probe(probeContext, providers.Credential{APIKey: target.Secret})
	if err != nil {
		result.Status = "failed"
		result.ErrorKind = stringPointer(canonicalErrorKind(err))
		return withLatency(result, startedAt)
	}
	if probe.Kind != "" {
		result.Kind = string(probe.Kind)
	}
	result.MayUseTokens = probe.MayConsumeTokens
	if !probe.Available || probe.MayConsumeTokens || probe.Request == nil {
		result.ErrorKind = stringPointer("probe_unavailable")
		return withLatency(result, startedAt)
	}
	client, err := security.NewSSRFSafeClient(e.policy)
	if err != nil {
		result.Status = "failed"
		result.ErrorKind = stringPointer(string(canonical.ErrorProviderConfiguration))
		return withLatency(result, startedAt)
	}
	response, err := client.Do(probe.Request)
	if err != nil {
		result.Status = "failed"
		result.ErrorKind = stringPointer(string(canonical.ErrorProviderTemporary))
		result.Retryable = true
		return withLatency(result, startedAt)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, e.maxResponseBytes+1))
	if err != nil {
		result.Status = "failed"
		result.ErrorKind = stringPointer(string(canonical.ErrorProviderTemporary))
		result.Retryable = true
		return withLatency(result, startedAt)
	}
	if int64(len(body)) > e.maxResponseBytes {
		result.Status = "failed"
		result.ErrorKind = stringPointer("probe_response_too_large")
		return withLatency(result, startedAt)
	}
	if providerError := adapter.ValidateProbe(probe.Kind, response.StatusCode, response.Header, body); providerError != nil {
		result.Status = "failed"
		result.ErrorKind = stringPointer(string(providerError.Kind))
		result.Retryable = providerError.Kind == canonical.ErrorProviderTemporary || providerError.Kind == canonical.ErrorRateLimit
		return withLatency(result, startedAt)
	}
	result.Status = "succeeded"
	return withLatency(result, startedAt)
}

func canonicalErrorKind(err error) string {
	var providerError *canonical.Error
	if errors.As(err, &providerError) {
		return string(providerError.Kind)
	}
	return string(canonical.ErrorProviderConfiguration)
}

func withLatency(result registry.CredentialProbeExecution, startedAt time.Time) registry.CredentialProbeExecution {
	result.LatencyMillis = max(time.Since(startedAt).Milliseconds(), 0)
	return result
}

func stringPointer(value string) *string {
	return &value
}
