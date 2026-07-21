package providers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/luckymaomi/llmgateway/internal/canonical"
)

type wireChatResponse struct {
	ID        string               `json:"id"`
	RequestID string               `json:"request_id"`
	Created   *int64               `json:"created"`
	Model     string               `json:"model"`
	Choices   []wireResponseChoice `json:"choices"`
	Usage     *wireUsage           `json:"usage"`
	Error     *wireError           `json:"error"`
}

type wireResponseChoice struct {
	Index        *int                `json:"index"`
	Message      wireResponseMessage `json:"message"`
	FinishReason *string             `json:"finish_reason"`
}

type wireResponseMessage struct {
	Role             canonical.Role `json:"role"`
	Content          *string        `json:"content"`
	ReasoningContent *string        `json:"reasoning_content"`
	ToolCalls        []wireToolCall `json:"tool_calls"`
}

type wireUsage struct {
	PromptTokens            *int64                       `json:"prompt_tokens"`
	CompletionTokens        *int64                       `json:"completion_tokens"`
	TotalTokens             *int64                       `json:"total_tokens"`
	PromptCacheHitTokens    *int64                       `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens   *int64                       `json:"prompt_cache_miss_tokens"`
	PromptTokensDetails     *wirePromptTokensDetails     `json:"prompt_tokens_details"`
	CompletionTokensDetails *wireCompletionTokensDetails `json:"completion_tokens_details"`
}

type wirePromptTokensDetails struct {
	CachedTokens *int64 `json:"cached_tokens"`
}

type wireCompletionTokensDetails struct {
	ReasoningTokens *int64 `json:"reasoning_tokens"`
}

type wireStreamChunk struct {
	ID        string             `json:"id"`
	RequestID string             `json:"request_id"`
	Created   *int64             `json:"created"`
	Model     string             `json:"model"`
	Choices   []wireStreamChoice `json:"choices"`
	Usage     *wireUsage         `json:"usage"`
	Error     *wireError         `json:"error"`
}

type wireStreamChoice struct {
	Index        *int            `json:"index"`
	Delta        wireStreamDelta `json:"delta"`
	FinishReason *string         `json:"finish_reason"`
}

type wireStreamDelta struct {
	Role             *canonical.Role     `json:"role"`
	Content          *string             `json:"content"`
	ReasoningContent *string             `json:"reasoning_content"`
	ToolCalls        []wireToolCallDelta `json:"tool_calls"`
}

type wireToolCallDelta struct {
	Index        *int                  `json:"index"`
	ID           string                `json:"id"`
	Type         string                `json:"type"`
	Function     wireToolFunctionDelta `json:"function"`
	ExtraContent *toolCallExtraContent `json:"extra_content,omitempty"`
}

type wireToolFunctionDelta struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func (a *openAIAdapter) ParseResponse(statusCode int, headers http.Header, body []byte) (canonical.ChatResponse, error) {
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return canonical.ChatResponse{}, a.ClassifyError(statusCode, headers, body)
	}
	var response wireChatResponse
	if err := decodeJSON(body, &response); err != nil {
		return canonical.ChatResponse{}, a.contractError("malformed_response", "provider returned malformed JSON", err)
	}
	if response.Error != nil {
		return canonical.ChatResponse{}, a.ClassifyError(statusCode, headers, body)
	}
	if response.ID == "" || response.Model == "" || response.Created == nil || *response.Created < 0 || len(response.Choices) == 0 {
		return canonical.ChatResponse{}, a.contractError("incomplete_response", "provider response is missing required completion fields", nil)
	}
	if a.policy.responseRequestIDBody && response.RequestID == "" {
		return canonical.ChatResponse{}, a.contractError("missing_request_id", "provider response is missing its request ID", nil)
	}

	choices := make([]canonical.ChatChoice, 0, len(response.Choices))
	for _, choice := range response.Choices {
		parsed, err := a.parseChoice(choice)
		if err != nil {
			return canonical.ChatResponse{}, err
		}
		choices = append(choices, parsed)
	}
	usage, err := parseUsage(response.Usage)
	if err != nil {
		return canonical.ChatResponse{}, a.contractError("invalid_usage", "provider returned invalid usage", err)
	}

	requestID := ""
	if a.policy.responseRequestIDBody {
		requestID = response.RequestID
	} else if a.policy.responseRequestIDHeader != "" {
		requestID = headers.Get(a.policy.responseRequestIDHeader)
	}
	return canonical.ChatResponse{
		ID:        response.ID,
		RequestID: requestID,
		Model:     response.Model,
		CreatedAt: time.Unix(*response.Created, 0).UTC(),
		Choices:   choices,
		Usage:     usage,
	}, nil
}

func (a *openAIAdapter) parseChoice(choice wireResponseChoice) (canonical.ChatChoice, error) {
	if choice.Index == nil || *choice.Index < 0 || choice.FinishReason == nil || !validRole(choice.Message.Role) {
		return canonical.ChatChoice{}, a.contractError("invalid_choice", "provider response choice is incomplete", nil)
	}
	if choice.Message.Role != canonical.RoleAssistant {
		return canonical.ChatChoice{}, a.contractError("invalid_choice_role", "provider response choice role is invalid", nil)
	}
	finishReason, finishError := a.parseFinishReason(*choice.FinishReason)
	if finishError != nil {
		return canonical.ChatChoice{}, finishError
	}
	message := canonical.Message{Role: choice.Message.Role}
	if choice.Message.Content != nil {
		message.Content = canonical.TextContent(*choice.Message.Content)
	}
	if choice.Message.ReasoningContent != nil && a.policy.capabilities.ReasoningContent {
		message.Reasoning = &canonical.ReasoningContent{Text: *choice.Message.ReasoningContent}
	}
	if len(choice.Message.ToolCalls) > 0 {
		if !a.policy.capabilities.Tools {
			return canonical.ChatChoice{}, a.contractError("unexpected_tool_calls", "provider returned unsupported tool calls", nil)
		}
		message.ToolCalls = make([]canonical.ToolCall, 0, len(choice.Message.ToolCalls))
		for _, toolCall := range choice.Message.ToolCalls {
			if toolCall.ID == "" || toolCall.Type != "function" || !toolNamePattern.MatchString(toolCall.Function.Name) {
				return canonical.ChatChoice{}, a.contractError("invalid_tool_call", "provider returned an invalid tool call", nil)
			}
			parsedCall := canonical.ToolCall{
				ID: toolCall.ID, Type: toolCall.Type,
				Function: canonical.ToolFunctionCall{Name: toolCall.Function.Name, Arguments: toolCall.Function.Arguments},
			}
			if a.policy.decodeToolCallMetadata != nil {
				metadata, err := a.policy.decodeToolCallMetadata(toolCall)
				if err != nil {
					return canonical.ChatChoice{}, err
				}
				parsedCall.ProviderMetadata = metadata
			}
			message.ToolCalls = append(message.ToolCalls, parsedCall)
		}
	}
	if len(message.Content) == 0 && len(message.ToolCalls) == 0 && message.Reasoning == nil {
		return canonical.ChatChoice{}, a.contractError("empty_choice", "provider returned an empty response choice", nil)
	}
	return canonical.ChatChoice{Index: *choice.Index, Message: message, FinishReason: finishReason}, nil
}

func (a *openAIAdapter) parseFinishReason(reason string) (canonical.FinishReason, error) {
	switch reason {
	case string(canonical.FinishReasonStop):
		return canonical.FinishReasonStop, nil
	case string(canonical.FinishReasonLength):
		return canonical.FinishReasonLength, nil
	case string(canonical.FinishReasonToolCalls):
		return canonical.FinishReasonToolCalls, nil
	case string(canonical.FinishReasonContentFilter):
		return canonical.FinishReasonContentFilter, nil
	default:
		if mapped, found := a.policy.finishReasons[reason]; found {
			return mapped, nil
		}
		if kind, found := a.policy.finishReasonErrors[reason]; found {
			return "", a.finishReasonError(reason, kind)
		}
		return "", a.finishReasonError(reason, canonical.ErrorProviderPermanent)
	}
}

func (a *openAIAdapter) finishReasonError(reason string, kind canonical.ErrorKind) *canonical.Error {
	return &canonical.Error{
		Kind: kind, Code: reason,
		Message: "provider could not complete the request", Provider: string(a.policy.kind),
	}
}

func parseUsage(usage *wireUsage) (*canonical.Usage, error) {
	if usage == nil {
		return nil, nil
	}
	parsed := &canonical.Usage{
		InputTokens:          usage.PromptTokens,
		OutputTokens:         usage.CompletionTokens,
		TotalTokens:          usage.TotalTokens,
		CachedInputTokens:    usage.PromptCacheHitTokens,
		CacheMissInputTokens: usage.PromptCacheMissTokens,
		Source:               canonical.UsageAuthoritative,
	}
	if usage.PromptTokensDetails != nil && parsed.CachedInputTokens == nil {
		parsed.CachedInputTokens = usage.PromptTokensDetails.CachedTokens
	}
	if usage.CompletionTokensDetails != nil {
		parsed.ReasoningTokens = usage.CompletionTokensDetails.ReasoningTokens
	}
	if parsed.InputTokens == nil && parsed.OutputTokens == nil && parsed.TotalTokens == nil &&
		parsed.CachedInputTokens == nil && parsed.CacheMissInputTokens == nil && parsed.ReasoningTokens == nil {
		return nil, errors.New("usage object has no token counters")
	}
	for _, count := range []*int64{parsed.InputTokens, parsed.OutputTokens, parsed.TotalTokens, parsed.CachedInputTokens, parsed.CacheMissInputTokens, parsed.ReasoningTokens} {
		if count != nil && *count < 0 {
			return nil, errors.New("usage contains a negative token counter")
		}
	}
	return parsed, nil
}

func decodeJSON(body []byte, target any) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return errors.New("empty JSON body")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return fmt.Errorf("trailing JSON data: %w", err)
	}
	return nil
}

func (a *openAIAdapter) contractError(code, message string, cause error) *canonical.Error {
	return &canonical.Error{
		Kind: canonical.ErrorProviderPermanent, Code: code, Message: message,
		Provider: string(a.policy.kind), Cause: cause,
	}
}
