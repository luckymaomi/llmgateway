package protocol

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/luckymaomi/llmgateway/internal/canonical"
)

type ErrorEnvelope struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Param   string `json:"param,omitempty"`
		Code    string `json:"code"`
	} `json:"error"`
	RequestID string `json:"request_id,omitempty"`
}

func PresentChatResponse(response canonical.ChatResponse) map[string]any {
	choices := make([]map[string]any, 0, len(response.Choices))
	for _, choice := range response.Choices {
		choices = append(choices, map[string]any{"index": choice.Index, "message": presentMessage(choice.Message), "finish_reason": choice.FinishReason})
	}
	result := map[string]any{"id": response.ID, "object": "chat.completion", "created": unixSeconds(response.CreatedAt), "model": response.Model, "choices": choices}
	if response.Usage != nil {
		result["usage"] = presentUsage(*response.Usage)
	}
	return result
}

func PresentStreamEvent(event canonical.StreamEvent) map[string]any {
	delta := map[string]any{}
	switch event.Type {
	case canonical.StreamMessageStart:
		delta["role"] = event.Role
	case canonical.StreamContentDelta:
		delta["content"] = event.ContentDelta
	case canonical.StreamReasoningDelta:
		delta["reasoning_content"] = event.ReasoningDelta
	case canonical.StreamToolCallDelta:
		if event.ToolCallDelta != nil {
			delta["tool_calls"] = []map[string]any{{"index": event.ToolCallDelta.Index, "id": event.ToolCallDelta.ID, "type": event.ToolCallDelta.Type, "function": map[string]any{"name": event.ToolCallDelta.FunctionName, "arguments": event.ToolCallDelta.ArgumentsFragment}}}
		}
	}
	choice := map[string]any{"index": event.ChoiceIndex, "delta": delta, "finish_reason": nil}
	if event.Type == canonical.StreamFinish {
		choice["finish_reason"] = event.FinishReason
	}
	result := map[string]any{"id": event.CompletionID, "object": "chat.completion.chunk", "created": time.Now().UTC().Unix(), "model": event.Model, "choices": []map[string]any{choice}}
	if event.Type == canonical.StreamUsage && event.Usage != nil {
		result["choices"] = []map[string]any{}
		result["usage"] = presentUsage(*event.Usage)
	}
	return result
}

func WriteSSE(writer io.Writer, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "data: %s\n\n", encoded)
	return err
}

func WriteNamedSSE(writer io.Writer, event string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "event: %s\n", event); err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "data: %s\n\n", encoded)
	return err
}

func WriteSSEDone(writer io.Writer) error {
	_, err := io.WriteString(writer, "data: [DONE]\n\n")
	return err
}

func WriteError(w http.ResponseWriter, requestID string, providerError *canonical.Error) {
	status := providerError.HTTPStatus
	if status < 400 || status > 599 {
		status = statusForError(providerError.Kind)
	}
	envelope := ErrorEnvelope{RequestID: requestID}
	envelope.Error.Message = providerError.Message
	envelope.Error.Type = string(providerError.Kind)
	envelope.Error.Param = providerError.Parameter
	envelope.Error.Code = providerError.Code
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(envelope)
}

func presentMessage(message canonical.Message) map[string]any {
	result := map[string]any{"role": message.Role, "content": messageText(message.Content)}
	if message.Reasoning != nil {
		result["reasoning_content"] = message.Reasoning.Text
	}
	if len(message.ToolCalls) > 0 {
		calls := make([]map[string]any, 0, len(message.ToolCalls))
		for _, call := range message.ToolCalls {
			calls = append(calls, map[string]any{"id": call.ID, "type": call.Type, "function": map[string]any{"name": call.Function.Name, "arguments": call.Function.Arguments}})
		}
		result["tool_calls"] = calls
	}
	return result
}

func messageText(parts []canonical.ContentPart) string {
	result := ""
	for _, part := range parts {
		if part.Type == canonical.ContentPartText {
			result += part.Text
		}
	}
	return result
}

func presentUsage(usage canonical.Usage) map[string]any {
	result := map[string]any{}
	if usage.InputTokens != nil {
		result["prompt_tokens"] = *usage.InputTokens
	}
	if usage.OutputTokens != nil {
		result["completion_tokens"] = *usage.OutputTokens
	}
	if usage.TotalTokens != nil {
		result["total_tokens"] = *usage.TotalTokens
	}
	if usage.CachedInputTokens != nil {
		result["prompt_tokens_details"] = map[string]any{"cached_tokens": *usage.CachedInputTokens}
	}
	if usage.ReasoningTokens != nil {
		result["completion_tokens_details"] = map[string]any{"reasoning_tokens": *usage.ReasoningTokens}
	}
	return result
}

func statusForError(kind canonical.ErrorKind) int {
	switch kind {
	case canonical.ErrorInvalidRequest, canonical.ErrorUnsupportedCapability:
		return http.StatusBadRequest
	case canonical.ErrorAuthentication:
		return http.StatusUnauthorized
	case canonical.ErrorPermission:
		return http.StatusForbidden
	case canonical.ErrorQuota:
		return http.StatusPaymentRequired
	case canonical.ErrorAdmissionTimeout, canonical.ErrorRateLimit:
		return http.StatusTooManyRequests
	case canonical.ErrorProviderTemporary, canonical.ErrorStorageUnavailable:
		return http.StatusServiceUnavailable
	case canonical.ErrorProviderConfiguration, canonical.ErrorProviderPermanent:
		return http.StatusBadGateway
	case canonical.ErrorStreamInterrupted, canonical.ErrorUncertain:
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}
