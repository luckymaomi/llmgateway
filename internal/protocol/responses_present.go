package protocol

import (
	"strconv"
	"strings"

	"github.com/luckymaomi/llmgateway/internal/canonical"
)

func PresentResponse(response canonical.ChatResponse, request ResponsesRequest) map[string]any {
	responseID := responseIdentifier(response.ID)
	return PresentResponseWithID(responseID, response, request)
}

func PresentResponseWithID(responseID string, response canonical.ChatResponse, request ResponsesRequest) map[string]any {
	output := make([]map[string]any, 0)
	outputText := ""
	for choiceIndex, choice := range response.Choices {
		if choice.Message.Reasoning != nil && choice.Message.Reasoning.Text != "" {
			output = append(output, map[string]any{
				"id": itemIdentifier("rs", responseID, choiceIndex), "type": "reasoning",
				"summary": []map[string]any{{"type": "summary_text", "text": choice.Message.Reasoning.Text}},
			})
		}
		text := messageText(choice.Message.Content)
		if text != "" {
			outputText += text
			output = append(output, responseMessageItem(itemIdentifier("msg", responseID, choiceIndex), text, "completed"))
		}
		for callIndex, call := range choice.Message.ToolCalls {
			output = append(output, map[string]any{
				"id": itemIdentifier("fc", responseID, choiceIndex*1000+callIndex), "type": "function_call", "status": "completed",
				"call_id": call.ID, "name": call.Function.Name, "arguments": call.Function.Arguments,
			})
		}
	}
	result := responseBase(responseID, response.Model, request, "completed")
	result["created_at"] = unixSeconds(response.CreatedAt)
	result["completed_at"] = unixSeconds(response.CreatedAt)
	result["output"] = output
	result["output_text"] = outputText
	if response.Usage != nil {
		result["usage"] = responseUsage(*response.Usage)
	}
	return result
}

func PresentResponseInProgress(responseID, model string, createdAt int64, request ResponsesRequest) map[string]any {
	result := responseBase(responseID, model, request, "in_progress")
	result["created_at"] = createdAt
	result["completed_at"] = nil
	return result
}

func PresentResponseFailed(responseID, model string, createdAt int64, request ResponsesRequest, providerError *canonical.Error) map[string]any {
	result := responseBase(responseID, model, request, "failed")
	result["created_at"] = createdAt
	result["completed_at"] = nil
	result["error"] = map[string]any{"code": providerError.Code, "message": providerError.Message}
	return result
}

func responseBase(id, model string, request ResponsesRequest, status string) map[string]any {
	result := map[string]any{
		"id": id, "object": "response", "status": status, "error": nil, "incomplete_details": nil,
		"instructions": nil, "max_output_tokens": request.Chat.MaxOutputTokens, "model": model, "output": []any{},
		"parallel_tool_calls": true, "previous_response_id": nil, "store": request.Store, "temperature": request.Chat.Temperature,
		"top_p": request.Chat.TopP, "truncation": "disabled", "usage": nil, "metadata": map[string]any{},
	}
	if request.Instructions != "" {
		result["instructions"] = request.Instructions
	}
	result["reasoning"] = map[string]any{"effort": reasoningEffort(request.Chat.Reasoning), "summary": nil}
	format := "text"
	if request.Chat.ResponseFormat != nil {
		format = string(request.Chat.ResponseFormat.Type)
	}
	result["text"] = map[string]any{"format": map[string]any{"type": format}}
	result["tool_choice"] = responseToolChoice(request.Chat.ToolChoice)
	result["tools"] = responseTools(request.Chat.Tools)
	return result
}

func responseMessageItem(id, text, status string) map[string]any {
	return map[string]any{
		"id": id, "type": "message", "status": status, "role": "assistant",
		"content": []map[string]any{{"type": "output_text", "text": text, "annotations": []any{}}},
	}
}

func responseUsage(usage canonical.Usage) map[string]any {
	input, output := int64(0), int64(0)
	if usage.InputTokens != nil {
		input = *usage.InputTokens
	}
	if usage.OutputTokens != nil {
		output = *usage.OutputTokens
	}
	reasoning := int64(0)
	if usage.ReasoningTokens != nil {
		reasoning = *usage.ReasoningTokens
	}
	return map[string]any{
		"input_tokens": input, "output_tokens": output, "total_tokens": input + output,
		"input_tokens_details":  map[string]any{"cached_tokens": valueOrZero(usage.CachedInputTokens)},
		"output_tokens_details": map[string]any{"reasoning_tokens": reasoning},
	}
}

func responseIdentifier(completionID string) string {
	if strings.HasPrefix(completionID, "resp_") {
		return completionID
	}
	completionID = strings.TrimPrefix(completionID, "chatcmpl-")
	completionID = strings.TrimPrefix(completionID, "chatcmpl_")
	return "resp_" + completionID
}

func ResponseIdentifierForRequest(requestID string) string {
	return "resp_" + strings.ReplaceAll(requestID, "-", "")
}

func itemIdentifier(prefix, responseID string, index int) string {
	return prefix + "_" + strings.TrimPrefix(responseID, "resp_") + "_" + strconv.Itoa(index)
}

func reasoningEffort(reasoning *canonical.ReasoningConfig) any {
	if reasoning == nil || reasoning.Effort == "" {
		return nil
	}
	return reasoning.Effort
}

func responseToolChoice(choice *canonical.ToolChoice) any {
	if choice == nil {
		return "auto"
	}
	if choice.Mode == canonical.ToolChoiceFunction {
		return map[string]any{"type": "function", "name": choice.FunctionName}
	}
	return choice.Mode
}

func responseTools(tools []canonical.ToolDefinition) []map[string]any {
	result := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		result = append(result, map[string]any{"type": "function", "name": tool.Name, "description": tool.Description, "parameters": tool.Parameters, "strict": tool.Strict})
	}
	return result
}

func valueOrZero(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}
