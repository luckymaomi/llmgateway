package canonical

import "encoding/json"

type ToolDefinition struct {
	Name        string
	Description string
	Parameters  json.RawMessage
	Strict      *bool
}

type ToolCall struct {
	ID       string
	Type     string
	Function ToolFunctionCall
}

type ToolFunctionCall struct {
	Name      string
	Arguments string
}

type ToolCallDelta struct {
	Index             int
	ID                string
	Type              string
	FunctionName      string
	ArgumentsFragment string
}

type ToolChoiceMode string

const (
	ToolChoiceNone     ToolChoiceMode = "none"
	ToolChoiceAuto     ToolChoiceMode = "auto"
	ToolChoiceRequired ToolChoiceMode = "required"
	ToolChoiceFunction ToolChoiceMode = "function"
)

type ToolChoice struct {
	Mode         ToolChoiceMode
	FunctionName string
}
