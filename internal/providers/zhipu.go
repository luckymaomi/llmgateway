package providers

import "github.com/luckymaomi/llmgateway/internal/canonical"

func NewZhipu() Adapter {
	return mustNewAdapter("https://open.bigmodel.cn/api/paas/v4", zhipuPolicy())
}

func NewZhipuWithBaseURL(baseURL string) (Adapter, error) {
	return newAdapter(baseURL, zhipuPolicy())
}

func zhipuPolicy() wirePolicy {
	return wirePolicy{
		kind: KindZhipu,
		capabilities: Capabilities{
			Chat: true, Models: true, Streaming: true, Tools: true, ToolStreaming: true, ToolChoiceAuto: true,
			JSONOutput: true, ReasoningToggle: true, ReasoningEffort: true, ReasoningContent: true,
			ReasoningReplay: true, ResponseUsage: true, ResponseRequestID: true,
		},
		chatPath: "chat/completions", modelsPath: "models", reasoning: reasoningWireZhipu,
		sendToolStream: true, responseRequestIDBody: true, maxStops: 4,
		maxOutputTokens: integerRange{set: true, min: 1, max: 131072},
		temperature:     numberRange{set: true, min: 0, max: 1}, topP: numberRange{set: true, min: 0.01, max: 1},
		finishReasons: map[string]canonical.FinishReason{"sensitive": canonical.FinishReasonContentFilter},
		finishReasonErrors: map[string]canonical.ErrorKind{
			"network_error": canonical.ErrorProviderTemporary, "model_context_window_exceeded": canonical.ErrorInvalidRequest,
		},
		classify: classifyZhipuError, retryAfter: standardRetryAfter,
	}
}

func classifyZhipuError(statusCode int, providerError *wireError) canonical.ErrorKind {
	code := ""
	if providerError != nil {
		code = string(providerError.Code)
	}
	switch code {
	case "1000", "1001", "1002", "1003", "1004", "1110", "1111", "1112":
		return canonical.ErrorAuthentication
	case "1113", "1304", "1308", "1309", "1310":
		return canonical.ErrorQuota
	case "1210", "1213", "1214", "1215", "1261":
		return canonical.ErrorInvalidRequest
	case "1211", "1221", "1222":
		return canonical.ErrorProviderConfiguration
	case "1212":
		return canonical.ErrorUnsupportedCapability
	case "1220", "1301", "1311":
		return canonical.ErrorPermission
	case "1302":
		return canonical.ErrorRateLimit
	case "500", "1120", "1230", "1234", "1305":
		return canonical.ErrorProviderTemporary
	case "1121", "1231", "1300":
		return canonical.ErrorProviderPermanent
	default:
		return classifyHTTPError(statusCode, providerError)
	}
}
