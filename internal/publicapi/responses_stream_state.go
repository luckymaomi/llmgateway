package publicapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/luckymaomi/llmgateway/internal/canonical"
	"github.com/luckymaomi/llmgateway/internal/protocol"
)

type responseStream struct {
	writer     http.ResponseWriter
	flusher    http.Flusher
	request    protocol.ResponsesRequest
	requestID  uuid.UUID
	responseID string
	model      string
	createdAt  int64
	sequence   int64
	started    bool
	nextOutput int
	message    responseMessageStream
	reasoning  responseReasoningStream
	tools      map[int]*responseToolStream
	usage      *canonical.Usage
	finish     canonical.FinishReason
}

type responseMessageStream struct {
	started     bool
	closed      bool
	id          string
	outputIndex int
	text        string
}

type responseReasoningStream struct {
	started     bool
	closed      bool
	id          string
	outputIndex int
	text        string
}

type responseToolStream struct {
	closed      bool
	id          string
	callID      string
	name        string
	arguments   string
	outputIndex int
}

func (s *responseStream) start(event canonical.StreamEvent) error {
	s.started = true
	s.createdAt = event.CreatedAt.Unix()
	if event.CreatedAt.IsZero() {
		s.createdAt = time.Now().UTC().Unix()
	}
	s.writer.Header().Set("Content-Type", "text/event-stream")
	s.writer.Header().Set("Cache-Control", "no-cache, no-transform")
	s.writer.Header().Set("X-Accel-Buffering", "no")
	s.writer.Header().Set("X-Gateway-Request-ID", s.requestID.String())
	s.writer.WriteHeader(http.StatusOK)
	base := protocol.PresentResponseInProgress(s.responseID, s.model, s.createdAt, s.request)
	if err := s.emit("response.created", map[string]any{"type": "response.created", "response": base}); err != nil {
		return err
	}
	return s.emit("response.in_progress", map[string]any{"type": "response.in_progress", "response": base})
}

func (s *responseStream) consume(event canonical.StreamEvent, complete func(json.RawMessage) error) error {
	switch event.Type {
	case canonical.StreamMessageStart:
		return s.ensureMessage()
	case canonical.StreamContentDelta:
		if err := s.ensureMessage(); err != nil {
			return err
		}
		s.message.text += event.ContentDelta
		return s.emit("response.output_text.delta", map[string]any{
			"type": "response.output_text.delta", "item_id": s.message.id, "output_index": s.message.outputIndex,
			"content_index": 0, "delta": event.ContentDelta, "logprobs": []any{},
		})
	case canonical.StreamReasoningDelta:
		if err := s.ensureReasoning(); err != nil {
			return err
		}
		s.reasoning.text += event.ReasoningDelta
		return s.emit("response.reasoning_summary_text.delta", map[string]any{
			"type": "response.reasoning_summary_text.delta", "item_id": s.reasoning.id,
			"output_index": s.reasoning.outputIndex, "summary_index": 0, "delta": event.ReasoningDelta,
		})
	case canonical.StreamToolCallDelta:
		return s.consumeToolDelta(event.ToolCallDelta)
	case canonical.StreamFinish:
		s.finish = event.FinishReason
		return s.closeOutput()
	case canonical.StreamUsage:
		s.usage = event.Usage
		return nil
	case canonical.StreamDone:
		if err := s.closeOutput(); err != nil {
			return err
		}
		response := s.completedResponse()
		encoded, err := json.Marshal(response)
		if err != nil {
			return err
		}
		if err := complete(encoded); err != nil {
			return err
		}
		return s.emit("response.completed", map[string]any{"type": "response.completed", "response": response})
	default:
		return nil
	}
}

func (s *responseStream) ensureMessage() error {
	if s.message.started {
		return nil
	}
	s.message = responseMessageStream{started: true, id: "msg_" + strings.TrimPrefix(s.responseID, "resp_"), outputIndex: s.allocateOutput()}
	if err := s.emit("response.output_item.added", map[string]any{
		"type": "response.output_item.added", "output_index": s.message.outputIndex,
		"item": map[string]any{"id": s.message.id, "type": "message", "status": "in_progress", "role": "assistant", "content": []any{}},
	}); err != nil {
		return err
	}
	return s.emit("response.content_part.added", map[string]any{
		"type": "response.content_part.added", "item_id": s.message.id, "output_index": s.message.outputIndex,
		"content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
	})
}

func (s *responseStream) ensureReasoning() error {
	if s.reasoning.started {
		return nil
	}
	s.reasoning = responseReasoningStream{started: true, id: "rs_" + strings.TrimPrefix(s.responseID, "resp_"), outputIndex: s.allocateOutput()}
	return s.emit("response.output_item.added", map[string]any{
		"type": "response.output_item.added", "output_index": s.reasoning.outputIndex,
		"item": map[string]any{"id": s.reasoning.id, "type": "reasoning", "summary": []any{}},
	})
}

func (s *responseStream) consumeToolDelta(delta *canonical.ToolCallDelta) error {
	if delta == nil {
		return nil
	}
	if s.tools == nil {
		s.tools = make(map[int]*responseToolStream)
	}
	tool := s.tools[delta.Index]
	if tool == nil {
		id := delta.ID
		if id == "" {
			id = fmt.Sprintf("fc_%s_%d", strings.TrimPrefix(s.responseID, "resp_"), delta.Index)
		}
		tool = &responseToolStream{id: id, callID: delta.ID, name: delta.FunctionName, outputIndex: s.allocateOutput()}
		s.tools[delta.Index] = tool
		if err := s.emit("response.output_item.added", map[string]any{
			"type": "response.output_item.added", "output_index": tool.outputIndex,
			"item": map[string]any{"id": tool.id, "type": "function_call", "status": "in_progress", "call_id": tool.callID, "name": tool.name, "arguments": ""},
		}); err != nil {
			return err
		}
	}
	if delta.ID != "" {
		tool.callID = delta.ID
	}
	if delta.FunctionName != "" {
		tool.name = delta.FunctionName
	}
	tool.arguments += delta.ArgumentsFragment
	return s.emit("response.function_call_arguments.delta", map[string]any{
		"type": "response.function_call_arguments.delta", "item_id": tool.id,
		"output_index": tool.outputIndex, "delta": delta.ArgumentsFragment,
	})
}

func (s *responseStream) closeOutput() error {
	if err := s.closeReasoning(); err != nil {
		return err
	}
	if err := s.closeMessage(); err != nil {
		return err
	}
	return s.closeTools()
}

func (s *responseStream) closeReasoning() error {
	if !s.reasoning.started || s.reasoning.closed {
		return nil
	}
	s.reasoning.closed = true
	if err := s.emit("response.reasoning_summary_text.done", map[string]any{
		"type": "response.reasoning_summary_text.done", "item_id": s.reasoning.id,
		"output_index": s.reasoning.outputIndex, "summary_index": 0, "text": s.reasoning.text,
	}); err != nil {
		return err
	}
	return s.emit("response.output_item.done", map[string]any{
		"type": "response.output_item.done", "output_index": s.reasoning.outputIndex,
		"item": map[string]any{"id": s.reasoning.id, "type": "reasoning", "summary": []map[string]any{{"type": "summary_text", "text": s.reasoning.text}}},
	})
}

func (s *responseStream) closeMessage() error {
	if !s.message.started || s.message.closed {
		return nil
	}
	s.message.closed = true
	if err := s.emit("response.output_text.done", map[string]any{
		"type": "response.output_text.done", "item_id": s.message.id, "output_index": s.message.outputIndex,
		"content_index": 0, "text": s.message.text, "logprobs": []any{},
	}); err != nil {
		return err
	}
	if err := s.emit("response.content_part.done", map[string]any{
		"type": "response.content_part.done", "item_id": s.message.id, "output_index": s.message.outputIndex,
		"content_index": 0, "part": map[string]any{"type": "output_text", "text": s.message.text, "annotations": []any{}},
	}); err != nil {
		return err
	}
	return s.emit("response.output_item.done", map[string]any{
		"type": "response.output_item.done", "output_index": s.message.outputIndex,
		"item": map[string]any{"id": s.message.id, "type": "message", "status": "completed", "role": "assistant", "content": []map[string]any{{"type": "output_text", "text": s.message.text, "annotations": []any{}}}},
	})
}

func (s *responseStream) closeTools() error {
	indices := make([]int, 0, len(s.tools))
	for index := range s.tools {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	for _, index := range indices {
		tool := s.tools[index]
		if tool.closed {
			continue
		}
		tool.closed = true
		if err := s.emit("response.function_call_arguments.done", map[string]any{
			"type": "response.function_call_arguments.done", "item_id": tool.id, "name": tool.name,
			"output_index": tool.outputIndex, "arguments": tool.arguments,
		}); err != nil {
			return err
		}
		if err := s.emit("response.output_item.done", map[string]any{
			"type": "response.output_item.done", "output_index": tool.outputIndex,
			"item": map[string]any{"id": tool.id, "type": "function_call", "status": "completed", "call_id": tool.callID, "name": tool.name, "arguments": tool.arguments},
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *responseStream) completedResponse() map[string]any {
	message := canonical.Message{Role: canonical.RoleAssistant}
	if s.message.started {
		message.Content = canonical.TextContent(s.message.text)
	}
	if s.reasoning.started {
		message.Reasoning = &canonical.ReasoningContent{Text: s.reasoning.text}
	}
	indices := make([]int, 0, len(s.tools))
	for index := range s.tools {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	for _, index := range indices {
		tool := s.tools[index]
		message.ToolCalls = append(message.ToolCalls, canonical.ToolCall{ID: tool.callID, Type: "function", Function: canonical.ToolFunctionCall{Name: tool.name, Arguments: tool.arguments}})
	}
	response := canonical.ChatResponse{
		ID: s.responseID, Model: s.model, CreatedAt: time.Unix(s.createdAt, 0).UTC(), Usage: s.usage,
		Choices: []canonical.ChatChoice{{Index: 0, Message: message, FinishReason: s.finish}},
	}
	return protocol.PresentResponseWithID(s.responseID, response, s.request)
}

func (s *responseStream) allocateOutput() int {
	index := s.nextOutput
	s.nextOutput++
	return index
}

func (s *responseStream) emit(name string, event map[string]any) error {
	s.sequence++
	event["sequence_number"] = s.sequence
	if err := protocol.WriteNamedSSE(s.writer, name, event); err != nil {
		return err
	}
	s.flusher.Flush()
	return nil
}
