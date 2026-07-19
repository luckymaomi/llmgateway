package canonical

import "time"

type StreamEventType string

const (
	StreamMessageStart   StreamEventType = "message_start"
	StreamContentDelta   StreamEventType = "content_delta"
	StreamReasoningDelta StreamEventType = "reasoning_delta"
	StreamToolCallDelta  StreamEventType = "tool_call_delta"
	StreamFinish         StreamEventType = "finish"
	StreamUsage          StreamEventType = "usage"
	StreamError          StreamEventType = "error"
	StreamDone           StreamEventType = "done"
)

type StreamEvent struct {
	Type           StreamEventType
	CompletionID   string
	RequestID      string
	Model          string
	CreatedAt      time.Time
	ChoiceIndex    int
	Role           Role
	ContentDelta   string
	ReasoningDelta string
	ToolCallDelta  *ToolCallDelta
	FinishReason   FinishReason
	Usage          *Usage
	Error          *Error
}
