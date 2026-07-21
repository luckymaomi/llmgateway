package canonical

import "encoding/json"

const MaxToolCallProviderMetadataBytes = 64 << 10

type ToolDefinition struct {
	Name        string
	Description string
	Parameters  json.RawMessage
	Strict      *bool
}

type ToolCall struct {
	ID               string
	Type             string
	Function         ToolFunctionCall
	ProviderMetadata *ToolCallProviderMetadata
}

// ToolCallProviderMetadata carries opaque Provider facts that a client must
// replay with an assistant tool call. Public protocol and Provider adapters
// own the wire shape; the canonical layer only preserves the bounded value.
type ToolCallProviderMetadata struct {
	GoogleThoughtSignature string
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
	ProviderMetadata  *ToolCallProviderMetadata
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
