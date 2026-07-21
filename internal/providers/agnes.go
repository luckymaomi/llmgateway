package providers

func NewAgnes() Adapter {
	return mustNewAdapter("https://apihub.agnes-ai.com/v1", agnesPolicy())
}

func NewAgnesWithBaseURL(baseURL string) (Adapter, error) {
	return newAdapter(baseURL, agnesPolicy())
}

func agnesPolicy() wirePolicy {
	return wirePolicy{
		kind: KindAgnes,
		capabilities: Capabilities{
			Chat: true, Models: true, Streaming: true, Tools: true, ReasoningToggle: true,
		},
		chatPath: "chat/completions", modelsPath: "models", reasoning: reasoningWireAgnes,
		maxStops: 4, maxOutputTokens: integerRange{set: true, min: 1},
		temperature: numberRange{set: true, min: 0, max: 2}, topP: numberRange{set: true, min: 0, max: 1},
		presencePenalty: numberRange{set: true, min: -2, max: 2}, frequencyPenalty: numberRange{set: true, min: -2, max: 2},
		classify: classifyHTTPError, retryAfter: standardRetryAfter,
	}
}
