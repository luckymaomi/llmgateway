package providers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/luckymaomi/llmgateway/internal/canonical"
)

func NewGemini() Adapter {
	return mustNewAdapter("https://generativelanguage.googleapis.com/v1beta/openai", geminiPolicy())
}

func NewGeminiWithBaseURL(baseURL string) (Adapter, error) {
	return newAdapter(baseURL, geminiPolicy())
}

func geminiPolicy() wirePolicy {
	return wirePolicy{
		kind: KindGemini,
		capabilities: Capabilities{
			Chat: true, Models: true, Streaming: true, Tools: true, ToolStreaming: true,
			ToolChoiceNone: true, ToolChoiceAuto: true, ToolChoiceRequired: true, ToolChoiceNamed: true,
			JSONOutput: true, ReasoningEffort: true, ResponseUsage: true, StreamUsage: true,
		},
		chatPath: "chat/completions", modelsPath: "models", reasoning: reasoningWireGemini,
		includeStreamUsage: true, maxTools: 128, maxStops: 5,
		maxOutputTokens: integerRange{set: true, min: 1, max: 65536},
		temperature:     numberRange{set: true, min: 0, max: 2}, topP: numberRange{set: true, min: 0, max: 1},
		allowedReasoningEfforts: map[canonical.ReasoningEffort]bool{
			canonical.ReasoningEffortNone: true, canonical.ReasoningEffortMinimal: true, canonical.ReasoningEffortLow: true,
			canonical.ReasoningEffortMedium: true, canonical.ReasoningEffortHigh: true,
		},
		transformToolSchema:    transformGeminiToolSchema,
		encodeToolCallMetadata: encodeGeminiToolCallMetadata,
		decodeToolCallMetadata: decodeGeminiToolCallMetadata,
		classify:               classifyGeminiError, retryAfter: geminiRetryAfter, replaySafe: geminiReplaySafe,
	}
}

func geminiReplaySafe(statusCode int, providerError *wireError) bool {
	if statusCode < http.StatusInternalServerError || statusCode > 599 {
		return false
	}
	return providerError == nil || providerError.Status == "" || providerError.Status == "UNAVAILABLE" || providerError.Status == "INTERNAL"
}

func encodeGeminiToolCallMetadata(target *wireToolCall, source canonical.ToolCall) error {
	if source.ProviderMetadata == nil || strings.TrimSpace(source.ProviderMetadata.GoogleThoughtSignature) == "" || len(source.ProviderMetadata.GoogleThoughtSignature) > canonical.MaxToolCallProviderMetadataBytes {
		return &canonical.Error{
			Kind: canonical.ErrorInvalidRequest, Code: "missing_google_thought_signature",
			Message: "Gemini assistant tool calls require their thought signature", Parameter: "messages.tool_calls.extra_content",
		}
	}
	target.ExtraContent = &toolCallExtraContent{Google: &googleToolCallMetadata{ThoughtSignature: source.ProviderMetadata.GoogleThoughtSignature}}
	return nil
}

func decodeGeminiToolCallMetadata(source wireToolCall) (*canonical.ToolCallProviderMetadata, error) {
	if source.ExtraContent == nil || source.ExtraContent.Google == nil || strings.TrimSpace(source.ExtraContent.Google.ThoughtSignature) == "" || len(source.ExtraContent.Google.ThoughtSignature) > canonical.MaxToolCallProviderMetadataBytes {
		return nil, &canonical.Error{
			Kind: canonical.ErrorProviderPermanent, Code: "missing_google_thought_signature",
			Message: "Gemini returned a tool call without its required thought signature", Provider: string(KindGemini),
		}
	}
	return &canonical.ToolCallProviderMetadata{GoogleThoughtSignature: source.ExtraContent.Google.ThoughtSignature}, nil
}

func transformGeminiToolSchema(raw json.RawMessage) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode Gemini tool schema: %w", err)
	}
	transformed := sanitizeGeminiSchema(value)
	encoded, err := json.Marshal(transformed)
	if err != nil {
		return nil, fmt.Errorf("encode Gemini tool schema: %w", err)
	}
	return encoded, nil
}

func sanitizeGeminiSchema(value any) any {
	switch typed := value.(type) {
	case []any:
		result := make([]any, len(typed))
		for index := range typed {
			result[index] = sanitizeGeminiSchema(typed[index])
		}
		return result
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[key] = sanitizeGeminiSchema(item)
		}
		types, hasTypes := result["type"].([]any)
		if hasTypes {
			var nonNull []string
			nullable := false
			for _, item := range types {
				name, ok := item.(string)
				if !ok {
					continue
				}
				if name == "null" {
					nullable = true
				} else {
					nonNull = append(nonNull, name)
				}
			}
			delete(result, "type")
			if len(nonNull) == 1 {
				result["type"] = nonNull[0]
			} else if len(nonNull) > 1 {
				variants := make([]any, 0, len(nonNull))
				for _, name := range nonNull {
					variants = append(variants, map[string]any{"type": name})
				}
				result["anyOf"] = variants
			}
			if nullable {
				result["nullable"] = true
			}
		}
		properties, isObject := result["properties"].(map[string]any)
		if result["type"] == "object" && isObject {
			if required, ok := result["required"].([]any); ok {
				filtered := make([]any, 0, len(required))
				for _, field := range required {
					name, ok := field.(string)
					if _, exists := properties[name]; ok && exists {
						filtered = append(filtered, name)
					}
				}
				result["required"] = filtered
			}
		}
		if result["type"] == "array" {
			if _, exists := result["items"]; !exists {
				result["items"] = map[string]any{}
			}
		}
		return result
	default:
		return value
	}
}

func classifyGeminiError(statusCode int, providerError *wireError) canonical.ErrorKind {
	if statusCode == http.StatusTooManyRequests && providerError != nil {
		quotaIDs := googleQuotaIDs(providerError)
		for _, quotaID := range quotaIDs {
			if strings.Contains(quotaID, "PerDay") || strings.Contains(quotaID, "RequestsPerDay") || strings.Contains(quotaID, "TokensPerDay") {
				return canonical.ErrorQuota
			}
		}
		for _, quotaID := range quotaIDs {
			if strings.Contains(quotaID, "PerMinute") || strings.Contains(quotaID, "RequestsPerMinute") || strings.Contains(quotaID, "TokensPerMinute") {
				return canonical.ErrorRateLimit
			}
		}
	}
	return classifyHTTPError(statusCode, providerError)
}

func geminiRetryAfter(headers http.Header, providerError *wireError) *canonical.RetryAfter {
	if retry := parseRetryAfter(headers); retry != nil {
		return retry
	}
	if providerError == nil {
		return nil
	}
	for _, detail := range providerError.Details {
		if !strings.HasSuffix(detail.Type, "google.rpc.RetryInfo") || len(detail.RetryDelay) == 0 {
			continue
		}
		var text string
		if json.Unmarshal(detail.RetryDelay, &text) == nil {
			if delay, err := time.ParseDuration(text); err == nil && delay >= 0 {
				seconds := int64(math.Ceil(delay.Seconds()))
				return &canonical.RetryAfter{DelaySeconds: &seconds}
			}
		}
	}
	return nil
}

func googleQuotaIDs(providerError *wireError) []string {
	var result []string
	for _, detail := range providerError.Details {
		if !strings.HasSuffix(detail.Type, "google.rpc.QuotaFailure") {
			continue
		}
		for _, violation := range detail.Violations {
			if violation.QuotaID != "" {
				result = append(result, violation.QuotaID)
			}
		}
	}
	return result
}
