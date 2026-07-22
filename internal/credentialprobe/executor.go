package credentialprobe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
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
	result := registry.CredentialProbeExecution{
		Kind: "generation", Status: "unavailable", MayUseTokens: true,
		ModelID: target.Model.ID, ModelName: target.Model.PublicName,
	}
	adapter, err := e.catalog.Build(target.Provider.Kind, providers.AdapterOptions{
		BaseURL: target.Provider.BaseURL, Capabilities: probeCapabilities(target.Model.Capabilities),
	})
	if err != nil {
		result.Status = "failed"
		result.ErrorKind = stringPointer(string(canonical.ErrorProviderConfiguration))
		return withLatency(result, startedAt)
	}
	probeContext, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	maxOutputTokens := int64(8)
	request, err := adapter.BuildRequest(probeContext, providers.Credential{APIKey: target.Secret}, canonical.ChatRequest{
		RequestID:       target.RequestID,
		Model:           target.Model.UpstreamName,
		Messages:        []canonical.Message{{Role: canonical.RoleUser, Content: canonical.TextContent("hi")}},
		MaxOutputTokens: &maxOutputTokens,
	})
	if err != nil {
		result.Status = "failed"
		result.ErrorKind = stringPointer(canonicalErrorKind(err))
		return withLatency(result, startedAt)
	}
	client, err := security.NewSSRFSafeClient(e.policy)
	if err != nil {
		result.Status = "failed"
		result.ErrorKind = stringPointer(string(canonical.ErrorProviderConfiguration))
		return withLatency(result, startedAt)
	}
	response, err := client.Do(request)
	if err != nil {
		result.Status = "failed"
		if probeOutcomeUnknown(err) {
			result.Status = "uncertain"
			result.ErrorKind = stringPointer(string(canonical.ErrorUncertain))
		} else {
			result.ErrorKind = stringPointer(string(canonical.ErrorProviderTemporary))
			result.Retryable = true
		}
		return withLatency(result, startedAt)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, e.maxResponseBytes+1))
	if err != nil {
		result.Status = "uncertain"
		result.ErrorKind = stringPointer(string(canonical.ErrorUncertain))
		return withLatency(result, startedAt)
	}
	if int64(len(body)) > e.maxResponseBytes {
		result.Status = "failed"
		result.ErrorKind = stringPointer("probe_response_too_large")
		return withLatency(result, startedAt)
	}
	parsed, err := adapter.ParseResponse(response.StatusCode, response.Header, body)
	if err != nil {
		result.Status = "failed"
		kind := canonicalErrorKind(err)
		result.ErrorKind = stringPointer(kind)
		result.Retryable = retryableKind(kind)
		return withLatency(result, startedAt)
	}
	result.ResponseText = responsePreview(parsed)
	if parsed.Usage != nil {
		result.InputTokens = parsed.Usage.InputTokens
		result.OutputTokens = parsed.Usage.OutputTokens
	}
	result.Status = "succeeded"
	return withLatency(result, startedAt)
}

func probeCapabilities(model registry.ModelCapabilities) providers.Capabilities {
	capabilities := providers.NarrowOpenAICompatibleCapabilities()
	capabilities.Streaming = false
	capabilities.Tools = false
	capabilities.ToolStreaming = false
	capabilities.ReasoningToggle = model.ReasoningMode == registry.ReasoningToggle || model.ReasoningMode == registry.ReasoningHybrid
	capabilities.ReasoningEffort = model.ReasoningMode == registry.ReasoningEffort || model.ReasoningMode == registry.ReasoningHybrid
	capabilities.ReasoningContent = model.Reasoning
	capabilities.ReasoningReplay = false
	capabilities.JSONOutput = false
	return capabilities
}

func responsePreview(response canonical.ChatResponse) string {
	if len(response.Choices) == 0 {
		return ""
	}
	var text strings.Builder
	for _, part := range response.Choices[0].Message.Content {
		if part.Type == canonical.ContentPartText {
			text.WriteString(part.Text)
		}
	}
	value := []rune(strings.TrimSpace(text.String()))
	if len(value) > 200 {
		value = value[:200]
	}
	return string(value)
}

func retryableKind(kind string) bool {
	return kind == string(canonical.ErrorProviderTemporary) || kind == string(canonical.ErrorRateLimit)
}

func probeOutcomeUnknown(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
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
