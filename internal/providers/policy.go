package providers

import "github.com/luckymaomi/llmgateway/internal/canonical"

type reasoningWire string

const (
	reasoningWireStandard reasoningWire = "standard"
	reasoningWireDeepSeek reasoningWire = "deepseek"
	reasoningWireZhipu    reasoningWire = "zhipu"
	reasoningWireAgnes    reasoningWire = "agnes"
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
	responseRequestIDHeader     string
	maxTools                    int
	maxStops                    int
	maxOutputTokens             integerRange
	temperature                 numberRange
	topP                        numberRange
	presencePenalty             numberRange
	frequencyPenalty            numberRange
	rejectSamplingWithReasoning bool
	classify                    func(int, string) canonical.ErrorKind
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
	}
}

func deepSeekPolicy() wirePolicy {
	return wirePolicy{
		kind: KindDeepSeek,
		capabilities: Capabilities{
			Chat:               true,
			Models:             true,
			Streaming:          true,
			Tools:              true,
			ToolStreaming:      true,
			ToolChoiceNone:     true,
			ToolChoiceAuto:     true,
			ToolChoiceRequired: true,
			ToolChoiceNamed:    true,
			StrictTools:        true,
			JSONOutput:         true,
			ReasoningToggle:    true,
			ReasoningEffort:    true,
			ReasoningContent:   true,
			ReasoningReplay:    true,
			ResponseUsage:      true,
			StreamUsage:        true,
		},
		chatPath:                    "chat/completions",
		modelsPath:                  "models",
		reasoning:                   reasoningWireDeepSeek,
		includeStreamUsage:          true,
		maxTools:                    128,
		maxStops:                    16,
		maxOutputTokens:             integerRange{set: true, min: 1},
		temperature:                 numberRange{set: true, min: 0, max: 2},
		topP:                        numberRange{set: true, min: 0, max: 1},
		presencePenalty:             numberRange{set: true, min: -2, max: 2},
		frequencyPenalty:            numberRange{set: true, min: -2, max: 2},
		rejectSamplingWithReasoning: true,
		classify:                    classifyDeepSeekError,
	}
}

func zhipuPolicy() wirePolicy {
	return wirePolicy{
		kind: KindZhipu,
		capabilities: Capabilities{
			Chat:              true,
			Streaming:         true,
			Tools:             true,
			ToolStreaming:     true,
			ToolChoiceAuto:    true,
			JSONOutput:        true,
			ReasoningToggle:   true,
			ReasoningEffort:   true,
			ReasoningContent:  true,
			ReasoningReplay:   true,
			ResponseUsage:     true,
			ResponseRequestID: true,
		},
		chatPath:              "chat/completions",
		reasoning:             reasoningWireZhipu,
		sendToolStream:        true,
		responseRequestIDBody: true,
		maxStops:              4,
		maxOutputTokens:       integerRange{set: true, min: 1, max: 131072},
		temperature:           numberRange{set: true, min: 0, max: 1},
		topP:                  numberRange{set: true, min: 0.01, max: 1},
		classify:              classifyZhipuError,
	}
}

func agnesPolicy() wirePolicy {
	return wirePolicy{
		kind: KindAgnes,
		capabilities: Capabilities{
			Chat:            true,
			Streaming:       true,
			Tools:           true,
			ReasoningToggle: true,
		},
		chatPath:         "chat/completions",
		reasoning:        reasoningWireAgnes,
		maxStops:         4,
		maxOutputTokens:  integerRange{set: true, min: 1},
		temperature:      numberRange{set: true, min: 0, max: 2},
		topP:             numberRange{set: true, min: 0, max: 1},
		presencePenalty:  numberRange{set: true, min: -2, max: 2},
		frequencyPenalty: numberRange{set: true, min: -2, max: 2},
		classify:         classifyHTTPError,
	}
}
