package providers

import (
	"encoding/json"
	"net/http"

	"github.com/luckymaomi/llmgateway/internal/canonical"
)

type reasoningWire string

const (
	reasoningWireStandard reasoningWire = "standard"
	reasoningWireZhipu    reasoningWire = "zhipu"
	reasoningWireAgnes    reasoningWire = "agnes"
	reasoningWireGemini   reasoningWire = "gemini"
)

type numberRange struct {
	set bool
	min float64
	max float64
}

type integerRange struct {
	set bool
	min int64
	max int64
}

type wirePolicy struct {
	kind                        Kind
	capabilities                Capabilities
	chatPath                    string
	modelsPath                  string
	reasoning                   reasoningWire
	includeStreamUsage          bool
	sendToolStream              bool
	responseRequestIDBody       bool
	streamRequestIDBody         bool
	responseRequestIDHeader     string
	maxTools                    int
	maxStops                    int
	maxOutputTokens             integerRange
	temperature                 numberRange
	topP                        numberRange
	presencePenalty             numberRange
	frequencyPenalty            numberRange
	rejectSamplingWithReasoning bool
	allowedReasoningEfforts     map[canonical.ReasoningEffort]bool
	finishReasons               map[string]canonical.FinishReason
	finishReasonErrors          map[string]canonical.ErrorKind
	transformToolSchema         func(json.RawMessage) (json.RawMessage, error)
	encodeToolCallMetadata      func(*wireToolCall, canonical.ToolCall) error
	decodeToolCallMetadata      func(wireToolCall) (*canonical.ToolCallProviderMetadata, error)
	classify                    func(int, *wireError) canonical.ErrorKind
	retryAfter                  func(http.Header, *wireError) *canonical.RetryAfter
	replaySafe                  func(int, *wireError) bool
}

func openAICompatiblePolicy(capabilities Capabilities, requestIDHeader string) wirePolicy {
	return wirePolicy{
		kind:                    KindOpenAICompatible,
		capabilities:            capabilities,
		chatPath:                "chat/completions",
		modelsPath:              "models",
		reasoning:               reasoningWireStandard,
		includeStreamUsage:      capabilities.StreamUsage,
		responseRequestIDHeader: requestIDHeader,
		maxTools:                128,
		maxStops:                4,
		maxOutputTokens:         integerRange{set: true, min: 1},
		temperature:             numberRange{set: true, min: 0, max: 2},
		topP:                    numberRange{set: true, min: 0, max: 1},
		presencePenalty:         numberRange{set: true, min: -2, max: 2},
		frequencyPenalty:        numberRange{set: true, min: -2, max: 2},
		classify:                classifyHTTPError,
		retryAfter:              standardRetryAfter,
	}
}
