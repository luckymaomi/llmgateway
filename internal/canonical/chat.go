package canonical

import "time"

type Role string

const (
	RoleSystem    Role = "system"
	RoleDeveloper Role = "developer"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role       Role
	Name       string
	Content    []ContentPart
	ToolCalls  []ToolCall
	ToolCallID string
	Reasoning  *ReasoningContent
}

type ResponseFormatType string

const (
	ResponseFormatText       ResponseFormatType = "text"
	ResponseFormatJSONObject ResponseFormatType = "json_object"
)

type ResponseFormat struct {
	Type ResponseFormatType
}

type ChatRequest struct {
	RequestID        string
	Model            string
	Messages         []Message
	Tools            []ToolDefinition
	ToolChoice       *ToolChoice
	Stream           bool
	MaxOutputTokens  *int64
	Temperature      *float64
	TopP             *float64
	PresencePenalty  *float64
	FrequencyPenalty *float64
	Stop             []string
	ResponseFormat   *ResponseFormat
	Reasoning        *ReasoningConfig
}

type FinishReason string

const (
	FinishReasonStop          FinishReason = "stop"
	FinishReasonLength        FinishReason = "length"
	FinishReasonToolCalls     FinishReason = "tool_calls"
	FinishReasonContentFilter FinishReason = "content_filter"
)

type ChatChoice struct {
	Index        int
	Message      Message
	FinishReason FinishReason
}

type ChatResponse struct {
	ID        string
	RequestID string
	Model     string
	CreatedAt time.Time
	Choices   []ChatChoice
	Usage     *Usage
}
