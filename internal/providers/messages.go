package providers

import (
	"fmt"
	"strings"

	"github.com/luckymaomi/llmgateway/internal/canonical"
)

func (a *openAIAdapter) encodeMessages(messages []canonical.Message) ([]wireMessage, error) {
	encoded := make([]wireMessage, 0, len(messages))
	for index, message := range messages {
		if !validRole(message.Role) {
			return nil, a.requestError(canonical.ErrorInvalidRequest, "invalid_role", "message role is invalid", fmt.Sprintf("messages[%d].role", index))
		}
		if message.Role == canonical.RoleDeveloper {
			return nil, a.unsupported(fmt.Sprintf("messages[%d].role", index))
		}
		if message.Name != "" && !a.policy.capabilities.MessageName {
			return nil, a.unsupported(fmt.Sprintf("messages[%d].name", index))
		}
		content, err := a.encodeContent(message.Role, message.Content, len(message.ToolCalls) > 0)
		if err != nil {
			return nil, a.requestError(canonical.ErrorInvalidRequest, "invalid_content", err.Error(), fmt.Sprintf("messages[%d].content", index))
		}
		if message.Role == canonical.RoleTool && strings.TrimSpace(message.ToolCallID) == "" {
			return nil, a.requestError(canonical.ErrorInvalidRequest, "missing_tool_call_id", "tool messages require tool_call_id", fmt.Sprintf("messages[%d].tool_call_id", index))
		}
		if message.Reasoning != nil && !a.policy.capabilities.ReasoningReplay {
			return nil, a.unsupported(fmt.Sprintf("messages[%d].reasoning", index))
		}
		toolCalls, err := a.encodeToolCalls(message.ToolCalls, index)
		if err != nil {
			return nil, err
		}
		wireMessage := wireMessage{
			Role:       message.Role,
			Name:       message.Name,
			Content:    content,
			ToolCalls:  toolCalls,
			ToolCallID: message.ToolCallID,
		}
		if message.Reasoning != nil {
			wireMessage.ReasoningContent = message.Reasoning.Text
		}
		encoded = append(encoded, wireMessage)
	}
	return encoded, nil
}

func (a *openAIAdapter) encodeContent(role canonical.Role, content []canonical.ContentPart, hasToolCalls bool) (any, error) {
	if len(content) == 0 {
		if role == canonical.RoleAssistant && hasToolCalls {
			return nil, nil
		}
		return nil, fmt.Errorf("message content is required")
	}
	if len(content) == 1 && content[0].Type == canonical.ContentPartText {
		return content[0].Text, nil
	}
	if !a.policy.capabilities.ImageInput {
		return nil, fmt.Errorf("provider cannot represent this content without loss")
	}
	if role != canonical.RoleUser {
		return nil, fmt.Errorf("content blocks are only supported for user messages")
	}
	parts := make([]wireContentPart, 0, len(content))
	for _, part := range content {
		switch part.Type {
		case canonical.ContentPartText:
			parts = append(parts, wireContentPart{Type: part.Type, Text: part.Text})
		case canonical.ContentPartImageURL:
			if part.ImageURL == nil || strings.TrimSpace(part.ImageURL.URL) == "" {
				return nil, fmt.Errorf("image_url content requires a URL")
			}
			parts = append(parts, wireContentPart{Type: part.Type, ImageURL: &wireImageURL{URL: part.ImageURL.URL, Detail: part.ImageURL.Detail}})
		default:
			return nil, fmt.Errorf("content part type %q is invalid", part.Type)
		}
	}
	return parts, nil
}

func (a *openAIAdapter) encodeToolCalls(toolCalls []canonical.ToolCall, messageIndex int) ([]wireToolCall, error) {
	if len(toolCalls) == 0 {
		return nil, nil
	}
	if !a.policy.capabilities.Tools {
		return nil, a.unsupported(fmt.Sprintf("messages[%d].tool_calls", messageIndex))
	}
	encoded := make([]wireToolCall, 0, len(toolCalls))
	for index, toolCall := range toolCalls {
		if toolCall.ID == "" || !toolNamePattern.MatchString(toolCall.Function.Name) {
			return nil, a.requestError(canonical.ErrorInvalidRequest, "invalid_tool_call", "tool call ID and function name are required", fmt.Sprintf("messages[%d].tool_calls[%d]", messageIndex, index))
		}
		toolType := toolCall.Type
		if toolType == "" {
			toolType = "function"
		}
		if toolType != "function" {
			return nil, a.unsupported(fmt.Sprintf("messages[%d].tool_calls[%d].type", messageIndex, index))
		}
		encoded = append(encoded, wireToolCall{
			ID: toolCall.ID, Type: toolType,
			Function: wireFunctionCall{Name: toolCall.Function.Name, Arguments: toolCall.Function.Arguments},
		})
	}
	return encoded, nil
}
