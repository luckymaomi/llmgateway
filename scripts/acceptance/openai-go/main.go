package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

type scenario struct {
	Name          string `json:"name"`
	Succeeded     bool   `json:"succeeded"`
	LatencyMillis int64  `json:"latencyMillis"`
	HTTPStatus    int    `json:"httpStatus,omitempty"`
	ErrorCode     string `json:"errorCode,omitempty"`
	ErrorType     string `json:"errorType,omitempty"`
}

type summary struct {
	SDK       string     `json:"sdk"`
	Version   string     `json:"version"`
	Succeeded bool       `json:"succeeded"`
	Scenarios []scenario `json:"scenarios"`
}

type validationFailure struct{ code string }

func (failure *validationFailure) Error() string { return failure.code }

func invalid(code string) error { return &validationFailure{code: code} }

func main() {
	baseURL := requiredEnvironment("LLMGATEWAY_SDK_BASE_URL")
	apiKey := requiredEnvironment("LLMGATEWAY_SDK_API_KEY")
	successModel := requiredEnvironment("LLMGATEWAY_SDK_SUCCESS_MODEL")
	streamModel := requiredEnvironment("LLMGATEWAY_SDK_STREAM_MODEL")
	errorModel := requiredEnvironment("LLMGATEWAY_SDK_ERROR_MODEL")
	defer os.Unsetenv("LLMGATEWAY_SDK_API_KEY")

	client := openai.NewClient(
		option.WithBaseURL(strings.TrimRight(baseURL, "/")+"/"),
		option.WithAPIKey(apiKey),
		option.WithMaxRetries(0),
	)
	result := summary{SDK: "github.com/openai/openai-go/v3", Version: "v3.44.0"}
	result.Scenarios = append(result.Scenarios, measure("models", func(ctx context.Context) error {
		page, err := client.Models.List(ctx)
		if err != nil {
			return err
		}
		foundSuccess := false
		foundStream := false
		for _, model := range page.Data {
			foundSuccess = foundSuccess || model.ID == successModel
			foundStream = foundStream || model.ID == streamModel
		}
		if !foundSuccess || !foundStream {
			return invalid("model_absent")
		}
		return nil
	}))
	result.Scenarios = append(result.Scenarios, measure("chat", func(ctx context.Context) error {
		completion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Model: successModel,
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("Reply with exactly OK."),
			},
			MaxTokens: openai.Int(256),
		})
		if err != nil {
			return err
		}
		if completion.ID == "" {
			return invalid("chat_id_missing")
		}
		if len(completion.Choices) == 0 {
			return invalid("chat_choices_missing")
		}
		if completion.Choices[0].Message.Content == "" {
			return invalid("chat_content_missing")
		}
		if completion.Usage.TotalTokens <= 0 {
			return invalid("chat_usage_missing")
		}
		return nil
	}))
	result.Scenarios = append(result.Scenarios, measure("chat_stream", func(ctx context.Context) error {
		params := openai.ChatCompletionNewParams{
			Model: streamModel,
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("Reply with exactly OK."),
			},
			MaxTokens: openai.Int(256),
		}
		params.SetExtraFields(map[string]any{"thinking": map[string]any{"type": "disabled"}})
		stream := client.Chat.Completions.NewStreaming(ctx, params)
		accumulator := openai.ChatCompletionAccumulator{}
		chunks := 0
		for stream.Next() {
			chunks++
			if !accumulator.AddChunk(stream.Current()) {
				return invalid("stream_chunk_rejected")
			}
		}
		if err := stream.Err(); err != nil {
			return err
		}
		if chunks == 0 {
			return invalid("stream_chunks_missing")
		}
		if len(accumulator.Choices) == 0 {
			return invalid("stream_choices_missing")
		}
		if accumulator.Choices[0].Message.Content == "" {
			return invalid("stream_content_missing")
		}
		return nil
	}))
	result.Scenarios = append(result.Scenarios, measure("responses", func(ctx context.Context) error {
		response, err := client.Responses.New(ctx, responses.ResponseNewParams{
			Model:           successModel,
			Input:           responses.ResponseNewParamsInputUnion{OfString: openai.String("Reply with exactly OK.")},
			MaxOutputTokens: openai.Int(256),
		})
		if err != nil {
			return err
		}
		if response.ID == "" {
			return invalid("responses_id_missing")
		}
		if response.OutputText() == "" {
			return invalid("responses_text_missing")
		}
		if response.Usage.TotalTokens <= 0 {
			return invalid("responses_usage_missing")
		}
		return nil
	}))
	result.Scenarios = append(result.Scenarios, measure("tools", func(ctx context.Context) error {
		completion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Model: successModel,
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("Call the lookup tool for Beijing. Do not answer directly."),
			},
			Tools: []openai.ChatCompletionToolUnionParam{{
				OfFunction: &openai.ChatCompletionFunctionToolParam{Function: shared.FunctionDefinitionParam{
					Name: "lookup", Description: openai.String("Look up a city"),
					Parameters: shared.FunctionParameters{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}, "required": []string{"city"}},
				}},
			}},
			ToolChoice: openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.String("auto")},
			MaxTokens:  openai.Int(256),
		})
		if err != nil {
			return err
		}
		if len(completion.Choices) == 0 {
			return invalid("tool_choices_missing")
		}
		if len(completion.Choices[0].Message.ToolCalls) == 0 {
			return invalid("tool_call_missing")
		}
		if completion.Choices[0].Message.ToolCalls[0].Function.Name != "lookup" {
			return invalid("tool_name_mismatch")
		}
		return nil
	}))
	result.Scenarios = append(result.Scenarios, measure("reasoning", func(ctx context.Context) error {
		params := openai.ChatCompletionNewParams{
			Model: successModel,
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("Reply with exactly OK after reasoning."),
			},
			MaxTokens: openai.Int(256),
		}
		params.SetExtraFields(map[string]any{"thinking": map[string]any{"type": "enabled"}})
		completion, err := client.Chat.Completions.New(ctx, params)
		if err != nil {
			return err
		}
		if len(completion.Choices) == 0 {
			return invalid("reasoning_choices_missing")
		}
		if completion.Usage.TotalTokens <= 0 {
			return invalid("reasoning_usage_missing")
		}
		return nil
	}))
	result.Scenarios = append(result.Scenarios, measureCancellation(client, successModel))
	result.Scenarios = append(result.Scenarios, measureProviderError(client, errorModel))

	result.Succeeded = true
	for _, item := range result.Scenarios {
		result.Succeeded = result.Succeeded && item.Succeeded
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, "could not encode Go SDK acceptance summary")
		os.Exit(2)
	}
	if !result.Succeeded {
		os.Exit(1)
	}
}

func measure(name string, action func(context.Context) error) scenario {
	startedAt := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	err := action(ctx)
	result := scenario{Name: name, Succeeded: err == nil, LatencyMillis: max(time.Since(startedAt).Milliseconds(), 0)}
	if err != nil {
		var validation *validationFailure
		if errors.As(err, &validation) {
			result.ErrorCode = validation.code
			result.ErrorType = "sdk_validation"
			return result
		}
		var apiError *openai.Error
		if errors.As(err, &apiError) {
			result.HTTPStatus = apiError.StatusCode
			result.ErrorCode = apiError.Code
			result.ErrorType = apiError.Type
		}
	}
	return result
}

func measureCancellation(client openai.Client, model string) scenario {
	startedAt := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(250*time.Millisecond, cancel)
	defer timer.Stop()
	_, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: model, Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("Wait before replying with OK.")},
	})
	return scenario{
		Name: "cancel", Succeeded: errors.Is(err, context.Canceled),
		LatencyMillis: max(time.Since(startedAt).Milliseconds(), 0),
	}
}

func measureProviderError(client openai.Client, model string) scenario {
	startedAt := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model: model, Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("Reply with exactly OK.")},
		MaxTokens: openai.Int(16),
	})
	result := scenario{Name: "provider_error", LatencyMillis: max(time.Since(startedAt).Milliseconds(), 0)}
	var apiError *openai.Error
	if errors.As(err, &apiError) {
		result.HTTPStatus = apiError.StatusCode
		result.ErrorCode = apiError.Code
		result.ErrorType = apiError.Type
		result.Succeeded = apiError.StatusCode == 429 && apiError.Code == "1113" && apiError.Type == "quota"
	}
	return result
}

func requiredEnvironment(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		fmt.Fprintln(os.Stderr, name+" is required")
		os.Exit(2)
	}
	return value
}
