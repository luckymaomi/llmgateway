package providers

import (
	"strconv"
	"unicode/utf8"

	"github.com/luckymaomi/llmgateway/internal/canonical"
)

func (a *openAIAdapter) encodeReasoning(reasoning *canonical.ReasoningConfig, request *wireChatRequest) error {
	if reasoning == nil {
		return nil
	}
	if reasoning.Enabled != nil && !a.policy.capabilities.ReasoningToggle {
		return a.unsupported("reasoning.enabled")
	}
	if reasoning.Effort != "" && !a.policy.capabilities.ReasoningEffort {
		return a.unsupported("reasoning.effort")
	}
	if reasoning.Preserve != nil && a.policy.reasoning != reasoningWireZhipu {
		return a.unsupported("reasoning.preserve")
	}
	if reasoning.Preserve != nil && reasoning.Enabled == nil {
		return a.requestError(canonical.ErrorInvalidRequest, "preserve_without_thinking", "reasoning preservation requires an explicit thinking mode", "reasoning.preserve")
	}
	if reasoning.Enabled != nil && !*reasoning.Enabled && (reasoning.Effort != "" || reasoning.Preserve != nil) {
		return a.requestError(canonical.ErrorInvalidRequest, "reasoning_options_without_thinking", "reasoning effort and preservation require thinking mode", "reasoning")
	}
	if reasoning.Effort != "" && !a.supportsReasoningEffort(reasoning.Effort) {
		return a.requestError(canonical.ErrorInvalidRequest, "invalid_reasoning_effort", "reasoning effort is invalid", "reasoning.effort")
	}

	switch a.policy.reasoning {
	case reasoningWireStandard:
		request.ReasoningEffort = string(reasoning.Effort)
	case reasoningWireDeepSeek:
		if reasoning.Enabled != nil {
			request.Thinking = &wireThinking{Type: enabledType(*reasoning.Enabled)}
		}
		request.ReasoningEffort = string(reasoning.Effort)
	case reasoningWireZhipu:
		if reasoning.Enabled != nil || reasoning.Preserve != nil {
			thinking := &wireThinking{}
			if reasoning.Enabled != nil {
				thinking.Type = enabledType(*reasoning.Enabled)
			}
			if reasoning.Preserve != nil {
				clearThinking := !*reasoning.Preserve
				thinking.ClearThinking = &clearThinking
			}
			request.Thinking = thinking
		}
		request.ReasoningEffort = string(reasoning.Effort)
	case reasoningWireAgnes:
		if reasoning.Enabled != nil {
			request.ChatTemplateKwargs = &wireChatTemplateKwargs{EnableThinking: *reasoning.Enabled}
		}
	}
	return nil
}

func (a *openAIAdapter) supportsReasoningEffort(effort canonical.ReasoningEffort) bool {
	if !validReasoningEffort(effort) {
		return false
	}
	if a.policy.reasoning != reasoningWireDeepSeek {
		return true
	}
	switch effort {
	case canonical.ReasoningEffortLow, canonical.ReasoningEffortMedium, canonical.ReasoningEffortHigh,
		canonical.ReasoningEffortXHigh, canonical.ReasoningEffortMax:
		return true
	default:
		return false
	}
}

func (a *openAIAdapter) validateParameters(request canonical.ChatRequest) error {
	if err := a.validateReasoningReplay(request); err != nil {
		return err
	}
	if err := a.validateInteger("max_tokens", request.MaxOutputTokens, a.policy.maxOutputTokens); err != nil {
		return err
	}
	if err := a.validateNumber("temperature", request.Temperature, a.policy.temperature); err != nil {
		return err
	}
	if err := a.validateNumber("top_p", request.TopP, a.policy.topP); err != nil {
		return err
	}
	if err := a.validateNumber("presence_penalty", request.PresencePenalty, a.policy.presencePenalty); err != nil {
		return err
	}
	if err := a.validateNumber("frequency_penalty", request.FrequencyPenalty, a.policy.frequencyPenalty); err != nil {
		return err
	}
	if a.policy.maxStops > 0 && len(request.Stop) > a.policy.maxStops {
		return a.requestError(canonical.ErrorInvalidRequest, "too_many_stop_sequences", "stop sequence count exceeds provider limit", "stop")
	}
	for _, stop := range request.Stop {
		if stop == "" {
			return a.requestError(canonical.ErrorInvalidRequest, "empty_stop_sequence", "stop sequences must not be empty", "stop")
		}
	}
	if request.ResponseFormat != nil {
		if !a.policy.capabilities.JSONOutput || (request.ResponseFormat.Type != canonical.ResponseFormatText && request.ResponseFormat.Type != canonical.ResponseFormatJSONObject) {
			return a.unsupported("response_format")
		}
	}
	if a.policy.responseRequestIDBody && request.RequestID != "" {
		requestIDLength := utf8.RuneCountInString(request.RequestID)
		if requestIDLength < 6 || requestIDLength > 64 {
			return a.requestError(canonical.ErrorInvalidRequest, "invalid_request_id", "request ID must contain 6-64 characters", "request_id")
		}
	}
	if a.policy.rejectSamplingWithReasoning && (request.Reasoning == nil || request.Reasoning.Enabled == nil || *request.Reasoning.Enabled) {
		if request.Temperature != nil || request.TopP != nil || request.PresencePenalty != nil || request.FrequencyPenalty != nil {
			return a.requestError(canonical.ErrorInvalidRequest, "sampling_with_thinking", "sampling parameters are not effective in thinking mode", "reasoning")
		}
	}
	return nil
}

func (a *openAIAdapter) validateReasoningReplay(request canonical.ChatRequest) error {
	replayRequired := false
	switch a.policy.reasoning {
	case reasoningWireDeepSeek:
		replayRequired = request.Reasoning == nil || request.Reasoning.Enabled == nil || *request.Reasoning.Enabled
	case reasoningWireZhipu:
		replayRequired = request.Reasoning != nil && request.Reasoning.Enabled != nil && *request.Reasoning.Enabled &&
			request.Reasoning.Preserve != nil && *request.Reasoning.Preserve
	}
	if !replayRequired {
		return nil
	}
	for index, message := range request.Messages {
		if message.Role == canonical.RoleAssistant && len(message.ToolCalls) > 0 && message.Reasoning == nil {
			return a.requestError(canonical.ErrorInvalidRequest, "missing_reasoning_replay", "assistant tool calls require their preserved reasoning content", "messages["+strconv.Itoa(index)+"].reasoning")
		}
	}
	return nil
}

func (a *openAIAdapter) validateNumber(parameter string, value *float64, validRange numberRange) error {
	if value == nil {
		return nil
	}
	if !validRange.set {
		return a.unsupported(parameter)
	}
	if *value < validRange.min || (validRange.max != 0 && *value > validRange.max) {
		return a.requestError(canonical.ErrorInvalidRequest, "parameter_out_of_range", parameter+" is outside the provider range", parameter)
	}
	return nil
}

func (a *openAIAdapter) validateInteger(parameter string, value *int64, validRange integerRange) error {
	if value == nil {
		return nil
	}
	if !validRange.set {
		return a.unsupported(parameter)
	}
	if *value < validRange.min || (validRange.max != 0 && *value > validRange.max) {
		return a.requestError(canonical.ErrorInvalidRequest, "parameter_out_of_range", parameter+" is outside the provider range", parameter)
	}
	return nil
}
