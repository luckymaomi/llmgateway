package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/luckymaomi/llmgateway/internal/canonical"
)

type ResponsesRequest struct {
	Chat         canonical.ChatRequest
	Instructions string
	Store        bool
	Input        json.RawMessage
}

type responsesWireRequest struct {
	Model           string          `json:"model"`
	Input           json.RawMessage `json:"input"`
	Instructions    string          `json:"instructions,omitempty"`
	Tools           []responseTool  `json:"tools,omitempty"`
	ToolChoice      json.RawMessage `json:"tool_choice,omitempty"`
	Stream          bool            `json:"stream,omitempty"`
	Store           *bool           `json:"store,omitempty"`
	Background      bool            `json:"background,omitempty"`
	MaxOutputTokens *int64          `json:"max_output_tokens,omitempty"`
	Temperature     *float64        `json:"temperature,omitempty"`
	TopP            *float64        `json:"top_p,omitempty"`
	Reasoning       *struct {
		Effort string `json:"effort,omitempty"`
	} `json:"reasoning,omitempty"`
	Text *struct {
		Format responseFormat `json:"format"`
	} `json:"text,omitempty"`
}

type responseTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      *bool           `json:"strict,omitempty"`
}

type responseInputItem struct {
	Type      string          `json:"type,omitempty"`
	Role      string          `json:"role,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	CallID    string          `json:"call_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Arguments string          `json:"arguments,omitempty"`
	Output    json.RawMessage `json:"output,omitempty"`
}

type responseContentPart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func ParseResponsesRequest(body io.Reader, requestID string) (ResponsesRequest, *canonical.Error) {
	decoder := json.NewDecoder(io.LimitReader(body, 8<<20))
	decoder.DisallowUnknownFields()
	var wire responsesWireRequest
	if err := decoder.Decode(&wire); err != nil {
		return ResponsesRequest{}, invalid("invalid_json", "request body must be one valid Responses object", "", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ResponsesRequest{}, invalid("invalid_json", "request body must contain exactly one JSON object", "", err)
	}
	if wire.Model == "" {
		return ResponsesRequest{}, invalid("missing_model", "model is required", "model", nil)
	}
	if wire.Background {
		return ResponsesRequest{}, invalid("unsupported_background", "background Responses are not supported", "background", nil)
	}
	messages, parseError := parseResponseInput(wire.Input)
	if parseError != nil {
		return ResponsesRequest{}, parseError
	}
	if wire.Instructions != "" {
		messages = append([]canonical.Message{{Role: canonical.RoleDeveloper, Content: canonical.TextContent(wire.Instructions)}}, messages...)
	}
	request := canonical.ChatRequest{
		RequestID: requestID, Model: wire.Model, Messages: messages, Stream: wire.Stream,
		MaxOutputTokens: wire.MaxOutputTokens, Temperature: wire.Temperature, TopP: wire.TopP,
	}
	if err := validateSampling(request); err != nil {
		return ResponsesRequest{}, err
	}
	for index, item := range wire.Tools {
		if item.Type != "function" || item.Name == "" || !json.Valid(item.Parameters) {
			return ResponsesRequest{}, invalid("invalid_tool", "only valid function tools are supported", fmt.Sprintf("tools.%d", index), nil)
		}
		request.Tools = append(request.Tools, canonical.ToolDefinition{Name: item.Name, Description: item.Description, Parameters: append(json.RawMessage(nil), item.Parameters...), Strict: item.Strict})
	}
	request.ToolChoice, parseError = parseResponseToolChoice(wire.ToolChoice)
	if parseError != nil {
		return ResponsesRequest{}, parseError
	}
	if wire.Reasoning != nil && wire.Reasoning.Effort != "" {
		request.Reasoning = &canonical.ReasoningConfig{Effort: canonical.ReasoningEffort(wire.Reasoning.Effort)}
	}
	if wire.Text != nil {
		switch wire.Text.Format.Type {
		case string(canonical.ResponseFormatText):
			request.ResponseFormat = &canonical.ResponseFormat{Type: canonical.ResponseFormatText}
		case string(canonical.ResponseFormatJSONObject):
			request.ResponseFormat = &canonical.ResponseFormat{Type: canonical.ResponseFormatJSONObject}
		default:
			return ResponsesRequest{}, invalid("unsupported_text_format", "text.format.type is not supported", "text.format.type", nil)
		}
	}
	store := true
	if wire.Store != nil {
		store = *wire.Store
	}
	return ResponsesRequest{Chat: request, Instructions: wire.Instructions, Store: store, Input: append(json.RawMessage(nil), wire.Input...)}, nil
}

func parseResponseInput(raw json.RawMessage) ([]canonical.Message, *canonical.Error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, invalid("missing_input", "input is required", "input", nil)
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if text == "" {
			return nil, invalid("empty_input", "input must not be empty", "input", nil)
		}
		return []canonical.Message{{Role: canonical.RoleUser, Content: canonical.TextContent(text)}}, nil
	}
	var items []responseInputItem
	if err := json.Unmarshal(raw, &items); err != nil || len(items) == 0 {
		return nil, invalid("invalid_input", "input must be text or a non-empty input item array", "input", err)
	}
	messages := make([]canonical.Message, 0, len(items))
	for index, item := range items {
		message, parseError := parseResponseInputItem(item)
		if parseError != nil {
			parseError.Parameter = fmt.Sprintf("input.%d.%s", index, parseError.Parameter)
			return nil, parseError
		}
		messages = append(messages, message)
	}
	return messages, nil
}

func parseResponseInputItem(item responseInputItem) (canonical.Message, *canonical.Error) {
	switch item.Type {
	case "", "message":
		role := canonical.Role(item.Role)
		if role != canonical.RoleUser && role != canonical.RoleAssistant && role != canonical.RoleSystem && role != canonical.RoleDeveloper {
			return canonical.Message{}, invalid("invalid_role", "message role is invalid", "role", nil)
		}
		text, err := responseContentText(item.Content)
		if err != nil || text == "" {
			return canonical.Message{}, invalid("unsupported_content", "only text Responses input is supported", "content", err)
		}
		return canonical.Message{Role: role, Content: canonical.TextContent(text)}, nil
	case "function_call":
		if item.CallID == "" || item.Name == "" || !json.Valid([]byte(item.Arguments)) {
			return canonical.Message{}, invalid("invalid_function_call", "function call replay is invalid", "", nil)
		}
		return canonical.Message{Role: canonical.RoleAssistant, ToolCalls: []canonical.ToolCall{{ID: item.CallID, Type: "function", Function: canonical.ToolFunctionCall{Name: item.Name, Arguments: item.Arguments}}}}, nil
	case "function_call_output":
		if item.CallID == "" {
			return canonical.Message{}, invalid("invalid_function_output", "function output requires call_id", "call_id", nil)
		}
		text, err := responseContentText(item.Output)
		if err != nil {
			return canonical.Message{}, invalid("unsupported_function_output", "only text function output is supported", "output", err)
		}
		return canonical.Message{Role: canonical.RoleTool, ToolCallID: item.CallID, Content: canonical.TextContent(text)}, nil
	default:
		return canonical.Message{}, invalid("unsupported_input_item", "input item type is not supported", "type", nil)
	}
}

func responseContentText(raw json.RawMessage) (string, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, nil
	}
	var parts []responseContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", err
	}
	for _, part := range parts {
		if part.Type != "input_text" && part.Type != "output_text" {
			return "", fmt.Errorf("unsupported content type %q", part.Type)
		}
		text += part.Text
	}
	return text, nil
}

func parseResponseToolChoice(raw json.RawMessage) (*canonical.ToolChoice, *canonical.Error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var mode string
	if json.Unmarshal(raw, &mode) == nil {
		switch canonical.ToolChoiceMode(mode) {
		case canonical.ToolChoiceNone, canonical.ToolChoiceAuto, canonical.ToolChoiceRequired:
			return &canonical.ToolChoice{Mode: canonical.ToolChoiceMode(mode)}, nil
		default:
			return nil, invalid("invalid_tool_choice", "tool_choice mode is invalid", "tool_choice", nil)
		}
	}
	var named struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &named); err != nil || named.Type != "function" || named.Name == "" {
		return nil, invalid("invalid_tool_choice", "named tool_choice is invalid", "tool_choice", err)
	}
	return &canonical.ToolChoice{Mode: canonical.ToolChoiceFunction, FunctionName: named.Name}, nil
}
