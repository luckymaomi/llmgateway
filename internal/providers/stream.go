package providers

import (
	"bytes"
	"errors"
	"net/http"
	"time"

	"github.com/luckymaomi/llmgateway/internal/canonical"
)

type chatStreamParser struct {
	adapter      *openAIAdapter
	decoder      *canonical.SSEDecoder
	done         bool
	failed       bool
	closed       bool
	completionID string
	requestID    string
	model        string
	createdAt    time.Time
}

func (a *openAIAdapter) ParseStream() StreamParser {
	return &chatStreamParser{adapter: a, decoder: canonical.NewSSEDecoder(canonical.DefaultMaxSSEEventBytes)}
}

func (p *chatStreamParser) Feed(chunk []byte) ([]canonical.StreamEvent, error) {
	if p.closed {
		return nil, errors.New("provider stream parser is closed")
	}
	if p.done {
		if len(bytes.TrimSpace(chunk)) == 0 {
			return nil, nil
		}
		return nil, p.adapter.contractError("data_after_done", "provider sent data after stream completion", nil)
	}
	sseEvents, err := p.decoder.Feed(chunk)
	if err != nil {
		return nil, p.adapter.contractError("malformed_sse", "provider returned malformed SSE", err)
	}
	return p.parseSSEEvents(sseEvents)
}

func (p *chatStreamParser) Close() ([]canonical.StreamEvent, error) {
	if p.closed {
		return nil, errors.New("provider stream parser is closed")
	}
	p.closed = true
	sseEvents, err := p.decoder.Close()
	if err != nil {
		return nil, p.adapter.contractError("malformed_sse", "provider returned malformed SSE", err)
	}
	events, err := p.parseSSEEvents(sseEvents)
	if err != nil {
		return nil, err
	}
	if !p.done && !p.failed {
		events = append(events, canonical.StreamEvent{
			Type: canonical.StreamError, CompletionID: p.completionID, RequestID: p.requestID, Model: p.model, CreatedAt: p.createdAt,
			Error: &canonical.Error{
				Kind: canonical.ErrorStreamInterrupted, Code: "stream_ended_before_done",
				Message: "provider stream ended before completion", Provider: string(p.adapter.policy.kind),
			},
		})
	}
	return events, nil
}

func (p *chatStreamParser) parseSSEEvents(sseEvents []canonical.SSEEvent) ([]canonical.StreamEvent, error) {
	var events []canonical.StreamEvent
	for _, sseEvent := range sseEvents {
		data := bytes.TrimSpace(sseEvent.Data)
		if bytes.Equal(data, []byte("[DONE]")) {
			if p.done {
				return nil, p.adapter.contractError("duplicate_done", "provider sent duplicate stream completion", nil)
			}
			p.done = true
			events = append(events, canonical.StreamEvent{
				Type: canonical.StreamDone, CompletionID: p.completionID, RequestID: p.requestID, Model: p.model, CreatedAt: p.createdAt,
			})
			continue
		}
		if p.done {
			return nil, p.adapter.contractError("data_after_done", "provider sent data after stream completion", nil)
		}
		var streamChunk wireStreamChunk
		if err := decodeJSON(data, &streamChunk); err != nil {
			return nil, p.adapter.contractError("malformed_stream_chunk", "provider returned malformed stream JSON", err)
		}
		if streamChunk.Error != nil {
			classified := p.adapter.classifyWireError(http.StatusOK, nil, streamChunk.Error, streamChunk.RequestID)
			events = append(events, canonical.StreamEvent{Type: canonical.StreamError, Error: classified})
			p.failed = true
			continue
		}
		if err := p.acceptMetadata(streamChunk.ID, streamChunk.RequestID, streamChunk.Model, streamChunk.Created); err != nil {
			return nil, err
		}
		for _, choice := range streamChunk.Choices {
			parsed, err := p.parseStreamChoice(choice)
			if err != nil {
				return nil, err
			}
			for _, event := range parsed {
				if event.Type == canonical.StreamError {
					p.failed = true
				}
			}
			events = append(events, parsed...)
		}
		usage, err := parseUsage(streamChunk.Usage)
		if err != nil {
			return nil, p.adapter.contractError("invalid_stream_usage", "provider returned invalid stream usage", err)
		}
		if usage != nil {
			events = append(events, p.event(canonical.StreamEvent{Type: canonical.StreamUsage, Usage: usage}))
		}
		// Some compatible Providers send metadata-only heartbeat chunks. Metadata
		// was validated above, so they are safe no-ops rather than contract errors.
	}
	return events, nil
}

func (p *chatStreamParser) acceptMetadata(completionID, requestID, model string, created *int64) error {
	if completionID == "" || model == "" || created == nil || *created < 0 || (p.adapter.policy.streamRequestIDBody && requestID == "") {
		return p.adapter.contractError("incomplete_stream_chunk", "provider stream chunk is missing completion metadata", nil)
	}
	if p.completionID != "" && p.completionID != completionID {
		return p.adapter.contractError("changed_completion_id", "provider changed completion ID during the stream", nil)
	}
	if p.model != "" && p.model != model {
		return p.adapter.contractError("changed_model", "provider changed model during the stream", nil)
	}
	createdAt := time.Unix(*created, 0).UTC()
	if !p.createdAt.IsZero() && p.createdAt != createdAt {
		return p.adapter.contractError("changed_created_at", "provider changed creation time during the stream", nil)
	}
	p.completionID = completionID
	p.model = model
	p.createdAt = createdAt
	if (p.adapter.policy.responseRequestIDBody || p.adapter.policy.streamRequestIDBody) && requestID != "" {
		if p.requestID != "" && p.requestID != requestID {
			return p.adapter.contractError("changed_request_id", "provider changed request ID during the stream", nil)
		}
		p.requestID = requestID
	}
	return nil
}

func (p *chatStreamParser) parseStreamChoice(choice wireStreamChoice) ([]canonical.StreamEvent, error) {
	if choice.Index == nil || *choice.Index < 0 {
		return nil, p.adapter.contractError("invalid_stream_choice", "provider stream choice is missing its index", nil)
	}
	if choice.FinishReason != nil {
		if mapped, found := p.adapter.policy.finishReasons[*choice.FinishReason]; found {
			return []canonical.StreamEvent{p.event(canonical.StreamEvent{
				Type: canonical.StreamFinish, ChoiceIndex: *choice.Index, FinishReason: mapped,
			})}, nil
		}
	}
	var events []canonical.StreamEvent
	if choice.Delta.Role != nil {
		if !validRole(*choice.Delta.Role) || *choice.Delta.Role != canonical.RoleAssistant {
			return nil, p.adapter.contractError("invalid_stream_role", "provider stream role is invalid", nil)
		}
		events = append(events, p.event(canonical.StreamEvent{
			Type: canonical.StreamMessageStart, ChoiceIndex: *choice.Index, Role: *choice.Delta.Role,
		}))
	}
	if choice.Delta.ReasoningContent != nil && p.adapter.policy.capabilities.ReasoningContent {
		events = append(events, p.event(canonical.StreamEvent{
			Type: canonical.StreamReasoningDelta, ChoiceIndex: *choice.Index, ReasoningDelta: *choice.Delta.ReasoningContent,
		}))
	}
	if choice.Delta.Content != nil {
		events = append(events, p.event(canonical.StreamEvent{
			Type: canonical.StreamContentDelta, ChoiceIndex: *choice.Index, ContentDelta: *choice.Delta.Content,
		}))
	}
	for _, toolCall := range choice.Delta.ToolCalls {
		if !p.adapter.policy.capabilities.Tools {
			return nil, p.adapter.contractError("unexpected_tool_delta", "provider returned unsupported tool call deltas", nil)
		}
		if toolCall.Index == nil || *toolCall.Index < 0 || (toolCall.Type != "" && toolCall.Type != "function") {
			return nil, p.adapter.contractError("invalid_tool_delta", "provider returned an invalid tool call delta", nil)
		}
		events = append(events, p.event(canonical.StreamEvent{
			Type:        canonical.StreamToolCallDelta,
			ChoiceIndex: *choice.Index,
			ToolCallDelta: &canonical.ToolCallDelta{
				Index: *toolCall.Index, ID: toolCall.ID, Type: toolCall.Type,
				FunctionName: toolCall.Function.Name, ArgumentsFragment: toolCall.Function.Arguments,
			},
		}))
		if toolCall.ExtraContent != nil && p.adapter.policy.decodeToolCallMetadata != nil {
			metadata, err := p.adapter.policy.decodeToolCallMetadata(wireToolCall{ExtraContent: toolCall.ExtraContent})
			if err != nil {
				return nil, err
			}
			events[len(events)-1].ToolCallDelta.ProviderMetadata = metadata
		}
	}
	if choice.FinishReason != nil {
		finishReason, err := p.adapter.parseFinishReason(*choice.FinishReason)
		if err != nil {
			var providerError *canonical.Error
			if errors.As(err, &providerError) {
				events = append(events, p.event(canonical.StreamEvent{Type: canonical.StreamError, ChoiceIndex: *choice.Index, Error: providerError}))
				return events, nil
			}
			return nil, err
		}
		events = append(events, p.event(canonical.StreamEvent{
			Type: canonical.StreamFinish, ChoiceIndex: *choice.Index, FinishReason: finishReason,
		}))
	}
	if len(events) == 0 {
		// A valid indexed choice with an empty delta is a compatibility heartbeat.
		return nil, nil
	}
	return events, nil
}

func (p *chatStreamParser) event(event canonical.StreamEvent) canonical.StreamEvent {
	event.CompletionID = p.completionID
	event.RequestID = p.requestID
	event.Model = p.model
	event.CreatedAt = p.createdAt
	return event
}
