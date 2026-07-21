package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/luckymaomi/llmgateway/internal/canonical"
	"github.com/luckymaomi/llmgateway/internal/providers"
	"github.com/luckymaomi/llmgateway/internal/security"
)

const maxCredentialBytes = 8192

type scenarioResult struct {
	Name          string `json:"name"`
	Succeeded     bool   `json:"succeeded"`
	HTTPStatus    int    `json:"httpStatus,omitempty"`
	LatencyMillis int64  `json:"latencyMillis"`
	ErrorKind     string `json:"errorKind,omitempty"`
	ErrorCode     string `json:"errorCode,omitempty"`
	UsagePresent  bool   `json:"usagePresent,omitempty"`
	ToolCalls     int    `json:"toolCalls,omitempty"`
	StreamEvents  int    `json:"streamEvents,omitempty"`
}

type canaryResult struct {
	ContractVersion int              `json:"contractVersion"`
	CheckedAt       time.Time        `json:"checkedAt"`
	ProviderKind    providers.Kind   `json:"providerKind"`
	BaseURL         string           `json:"baseUrl"`
	Model           string           `json:"model"`
	Succeeded       bool             `json:"succeeded"`
	Scenarios       []scenarioResult `json:"scenarios"`
	ModelIDs        []string         `json:"modelIds,omitempty"`
}

type runner struct {
	adapter          providers.Adapter
	client           *http.Client
	credential       providers.Credential
	model            string
	scenarios        map[string]bool
	requestTimeout   time.Duration
	maxResponseBytes int64
	includeModelIDs  bool
	observedModelIDs []string
}

func main() {
	kind := flag.String("kind", "", "Provider kind registered in the catalog")
	baseURL := flag.String("base-url", "", "Provider API base URL")
	model := flag.String("model", "", "Upstream model ID used for generating scenarios")
	requestTimeout := flag.Duration("request-timeout", 90*time.Second, "Timeout for each Provider request")
	maxResponseBytes := flag.Int64("max-response-bytes", 16<<20, "Maximum Provider response bytes")
	allowedResolvedNetworks := flag.String("allowed-resolved-networks", "", "Comma-separated CIDRs explicitly allowed after Provider DNS resolution")
	scenarioNames := flag.String("scenarios", "models,chat,stream,tools,reasoning", "Comma-separated canary scenarios")
	includeModelIDs := flag.Bool("include-model-ids", false, "Include public model IDs returned by the models probe")
	flag.Parse()

	if strings.TrimSpace(*kind) == "" || strings.TrimSpace(*baseURL) == "" || strings.TrimSpace(*model) == "" || *requestTimeout <= 0 || *maxResponseBytes < 1024 {
		fmt.Fprintln(os.Stderr, "kind, base-url, model, and positive request bounds are required")
		os.Exit(2)
	}
	secret, err := readCredential(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not read Provider credential from stdin")
		os.Exit(2)
	}
	defer clearBytes(secret)

	adapter, err := providers.DefaultCatalog().Build(providers.Kind(*kind), providers.AdapterOptions{
		BaseURL: strings.TrimSpace(*baseURL), Capabilities: providers.NarrowOpenAICompatibleCapabilities(),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not build Provider adapter")
		os.Exit(2)
	}
	resolvedPrefixes, err := parsePrefixes(*allowedResolvedNetworks)
	if err != nil {
		fmt.Fprintln(os.Stderr, "allowed-resolved-networks contains an invalid CIDR")
		os.Exit(2)
	}
	client, err := security.NewSSRFSafeClient(security.SSRFPolicy{MaxRedirects: 3, AllowedResolvedPrefixes: resolvedPrefixes})
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not build Provider network client")
		os.Exit(2)
	}
	selectedScenarios, err := parseScenarios(*scenarioNames)
	if err != nil {
		fmt.Fprintln(os.Stderr, "scenarios must select models, chat, stream, tools, or reasoning")
		os.Exit(2)
	}
	canary := runner{
		adapter: adapter, client: client, credential: providers.Credential{APIKey: string(secret)}, model: strings.TrimSpace(*model),
		scenarios: selectedScenarios, requestTimeout: *requestTimeout, maxResponseBytes: *maxResponseBytes, includeModelIDs: *includeModelIDs,
	}
	result := canary.run(providers.Kind(*kind), strings.TrimSpace(*baseURL))
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, "could not encode canary result")
		os.Exit(2)
	}
	if !result.Succeeded {
		os.Exit(1)
	}
}

func parseScenarios(value string) (map[string]bool, error) {
	allowed := map[string]bool{"models": true, "chat": true, "stream": true, "tools": true, "reasoning": true}
	selected := make(map[string]bool)
	for _, raw := range strings.Split(value, ",") {
		name := strings.TrimSpace(raw)
		if !allowed[name] {
			return nil, errors.New("unknown canary scenario")
		}
		selected[name] = true
	}
	if len(selected) == 0 {
		return nil, errors.New("at least one canary scenario is required")
	}
	return selected, nil
}

func parsePrefixes(value string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	for _, raw := range strings.Split(value, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return nil, err
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}

func readCredential(reader io.Reader) ([]byte, error) {
	value, err := io.ReadAll(io.LimitReader(reader, maxCredentialBytes+1))
	if err != nil || len(value) > maxCredentialBytes {
		return nil, errors.New("credential input is unavailable or too large")
	}
	value = []byte(strings.TrimSpace(string(value)))
	if len(value) < 8 {
		clearBytes(value)
		return nil, errors.New("credential input is empty")
	}
	return value, nil
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func (r *runner) run(kind providers.Kind, baseURL string) canaryResult {
	result := canaryResult{
		ContractVersion: 1, CheckedAt: time.Now().UTC(), ProviderKind: kind, BaseURL: baseURL, Model: r.model,
	}
	if r.scenarios["models"] {
		result.Scenarios = append(result.Scenarios, r.probeModels())
	}
	if r.scenarios["chat"] {
		result.Scenarios = append(result.Scenarios, r.chat("chat", canonical.ChatRequest{
			Model: r.model, Messages: []canonical.Message{{Role: canonical.RoleUser, Content: canonical.TextContent("Reply with exactly OK.")}},
		}))
	}
	capabilities := r.adapter.Capabilities()
	if r.scenarios["stream"] && capabilities.Streaming {
		result.Scenarios = append(result.Scenarios, r.stream())
	}
	if r.scenarios["tools"] && capabilities.Tools {
		result.Scenarios = append(result.Scenarios, r.tools())
	}
	if r.scenarios["reasoning"] && (capabilities.ReasoningToggle || capabilities.ReasoningEffort) {
		request := canonical.ChatRequest{
			Model: r.model, Messages: []canonical.Message{{Role: canonical.RoleUser, Content: canonical.TextContent("Reply with exactly OK.")}},
			Reasoning: &canonical.ReasoningConfig{},
		}
		if capabilities.ReasoningToggle {
			enabled := true
			request.Reasoning.Enabled = &enabled
		}
		if capabilities.ReasoningEffort {
			request.Reasoning.Effort = canonical.ReasoningEffortLow
		}
		result.Scenarios = append(result.Scenarios, r.chat("reasoning", request))
	}
	result.Succeeded = true
	for _, scenario := range result.Scenarios {
		if !scenario.Succeeded {
			result.Succeeded = false
			break
		}
	}
	result.ModelIDs = append([]string(nil), r.observedModelIDs...)
	return result
}

func (r *runner) probeModels() scenarioResult {
	return r.measure("models", func(ctx context.Context) scenarioResult {
		probe, err := r.adapter.Probe(ctx, r.credential)
		if err != nil {
			return failedScenario(err, 0)
		}
		if !probe.Available || probe.MayConsumeTokens || probe.Request == nil {
			return scenarioResult{ErrorKind: "probe_unavailable"}
		}
		response, err := r.client.Do(probe.Request)
		if err != nil {
			return failedScenario(err, 0)
		}
		defer response.Body.Close()
		body, err := readBounded(response.Body, r.maxResponseBytes)
		if err != nil {
			return failedScenario(err, response.StatusCode)
		}
		if providerError := r.adapter.ValidateProbe(probe.Kind, response.StatusCode, response.Header, body); providerError != nil {
			return failedScenario(providerError, response.StatusCode)
		}
		if r.includeModelIDs {
			var listing struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if err := json.Unmarshal(body, &listing); err != nil || len(listing.Data) > 5000 {
				return scenarioResult{HTTPStatus: response.StatusCode, ErrorKind: "invalid_model_catalog"}
			}
			for _, model := range listing.Data {
				if strings.TrimSpace(model.ID) != "" && len(model.ID) <= 512 {
					r.observedModelIDs = append(r.observedModelIDs, model.ID)
				}
			}
			sort.Strings(r.observedModelIDs)
		}
		return scenarioResult{Succeeded: true, HTTPStatus: response.StatusCode}
	})
}

func (r runner) chat(name string, request canonical.ChatRequest) scenarioResult {
	return r.measure(name, func(ctx context.Context) scenarioResult {
		request.RequestID = "provider-canary"
		upstream, err := r.adapter.BuildRequest(ctx, r.credential, request)
		if err != nil {
			return failedScenario(err, 0)
		}
		response, err := r.client.Do(upstream)
		if err != nil {
			return failedScenario(err, 0)
		}
		defer response.Body.Close()
		body, err := readBounded(response.Body, r.maxResponseBytes)
		if err != nil {
			return failedScenario(err, response.StatusCode)
		}
		parsed, err := r.adapter.ParseResponse(response.StatusCode, response.Header, body)
		if err != nil {
			return failedScenario(err, response.StatusCode)
		}
		toolCalls := 0
		for _, choice := range parsed.Choices {
			toolCalls += len(choice.Message.ToolCalls)
		}
		return scenarioResult{Succeeded: true, HTTPStatus: response.StatusCode, UsagePresent: parsed.Usage != nil, ToolCalls: toolCalls}
	})
}

func (r runner) tools() scenarioResult {
	parameters := json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`)
	request := canonical.ChatRequest{
		Model:    r.model,
		Messages: []canonical.Message{{Role: canonical.RoleUser, Content: canonical.TextContent("Call lookup_weather for Beijing. Do not answer directly.")}},
		Tools:    []canonical.ToolDefinition{{Name: "lookup_weather", Description: "Look up weather by city", Parameters: parameters}},
	}
	result := r.chat("tools", request)
	if result.Succeeded && result.ToolCalls == 0 {
		result.Succeeded = false
		result.ErrorKind = "tool_call_missing"
	}
	return result
}

func (r runner) stream() scenarioResult {
	return r.measure("stream", func(ctx context.Context) scenarioResult {
		request := canonical.ChatRequest{
			RequestID: "provider-canary", Model: r.model, Stream: true,
			Messages: []canonical.Message{{Role: canonical.RoleUser, Content: canonical.TextContent("Reply with exactly OK.")}},
		}
		upstream, err := r.adapter.BuildRequest(ctx, r.credential, request)
		if err != nil {
			return failedScenario(err, 0)
		}
		response, err := r.client.Do(upstream)
		if err != nil {
			return failedScenario(err, 0)
		}
		defer response.Body.Close()
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			body, readErr := readBounded(response.Body, r.maxResponseBytes)
			if readErr != nil {
				return failedScenario(readErr, response.StatusCode)
			}
			return failedScenario(r.adapter.ClassifyError(response.StatusCode, response.Header, body), response.StatusCode)
		}
		parser := r.adapter.ParseStream()
		buffer := make([]byte, 32<<10)
		totalBytes := int64(0)
		eventCount := 0
		usagePresent := false
		for {
			count, readErr := response.Body.Read(buffer)
			if count > 0 {
				totalBytes += int64(count)
				if totalBytes > r.maxResponseBytes {
					return scenarioResult{HTTPStatus: response.StatusCode, ErrorKind: "response_too_large"}
				}
				events, parseErr := parser.Feed(buffer[:count])
				if parseErr != nil {
					return failedScenario(parseErr, response.StatusCode)
				}
				for _, event := range events {
					eventCount++
					usagePresent = usagePresent || event.Usage != nil
					if event.Error != nil {
						return failedScenario(event.Error, response.StatusCode)
					}
				}
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				return failedScenario(readErr, response.StatusCode)
			}
		}
		events, err := parser.Close()
		if err != nil {
			return failedScenario(err, response.StatusCode)
		}
		for _, event := range events {
			eventCount++
			usagePresent = usagePresent || event.Usage != nil
			if event.Error != nil {
				return failedScenario(event.Error, response.StatusCode)
			}
		}
		return scenarioResult{Succeeded: true, HTTPStatus: response.StatusCode, UsagePresent: usagePresent, StreamEvents: eventCount}
	})
}

func (r runner) measure(name string, action func(context.Context) scenarioResult) scenarioResult {
	startedAt := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), r.requestTimeout)
	defer cancel()
	result := action(ctx)
	result.Name = name
	result.LatencyMillis = max(time.Since(startedAt).Milliseconds(), 0)
	return result
}

func failedScenario(err error, status int) scenarioResult {
	result := scenarioResult{HTTPStatus: status, ErrorKind: "transport_error"}
	var providerError *canonical.Error
	if errors.As(err, &providerError) {
		result.ErrorKind = string(providerError.Kind)
		result.ErrorCode = providerError.Code
		if result.HTTPStatus == 0 {
			result.HTTPStatus = providerError.HTTPStatus
		}
		return result
	}
	var policyError *security.URLPolicyError
	if errors.As(err, &policyError) {
		result.ErrorKind = "url_policy_" + string(policyError.Kind)
		return result
	}
	if errors.Is(err, context.DeadlineExceeded) {
		result.ErrorKind = "timeout"
	}
	return result
}

func readBounded(reader io.Reader, maxBytes int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, errors.New("Provider response exceeds the canary byte limit")
	}
	return body, nil
}
