package requestflow

import (
	"encoding/json"

	"github.com/luckymaomi/llmgateway/internal/canonical"
)

func EstimateTokens(request canonical.ChatRequest) int64 {
	return EstimateInputTokens(request) + estimateOutputBudget(request)
}

func EstimateInputTokens(request canonical.ChatRequest) int64 {
	var bytes int64
	for _, message := range request.Messages {
		bytes += 8
		for _, part := range message.Content {
			bytes += int64(len(part.Text))
		}
		for _, call := range message.ToolCalls {
			bytes += int64(len(call.ID) + len(call.Function.Name) + len(call.Function.Arguments))
		}
		if message.Reasoning != nil {
			bytes += int64(len(message.Reasoning.Text))
		}
	}
	for _, tool := range request.Tools {
		bytes += int64(len(tool.Name) + len(tool.Description) + len(tool.Parameters))
	}
	input := bytes / 4
	if bytes%4 != 0 {
		input++
	}
	if input < 1 {
		input = 1
	}
	return input
}

func estimateOutputBudget(request canonical.ChatRequest) int64 {
	output := int64(1024)
	if request.MaxOutputTokens != nil {
		output = *request.MaxOutputTokens
	}
	return output
}

func RequestDigest(request canonical.ChatRequest) ([]byte, error) {
	// Canonical requests contain no maps except raw JSON schemas, so JSON is a
	// stable request identity after the public parser has normalized the wire.
	return json.Marshal(request)
}

func EstimatedOutputTokens(events []canonical.StreamEvent) int64 {
	var bytes int64
	for _, event := range events {
		bytes += int64(len(event.ContentDelta) + len(event.ReasoningDelta))
		if event.ToolCallDelta != nil {
			bytes += int64(len(event.ToolCallDelta.FunctionName) + len(event.ToolCallDelta.ArgumentsFragment))
		}
	}
	if bytes == 0 {
		return 0
	}
	return (bytes + 3) / 4
}
