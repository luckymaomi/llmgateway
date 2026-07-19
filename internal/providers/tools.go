package providers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/luckymaomi/llmgateway/internal/canonical"
)

func (a *openAIAdapter) encodeTools(tools []canonical.ToolDefinition) ([]wireTool, error) {
	if len(tools) == 0 {
		return nil, nil
	}
	if !a.policy.capabilities.Tools {
		return nil, a.unsupported("tools")
	}
	if a.policy.maxTools > 0 && len(tools) > a.policy.maxTools {
		return nil, a.requestError(canonical.ErrorInvalidRequest, "too_many_tools", "tool count exceeds provider limit", "tools")
	}
	encoded := make([]wireTool, 0, len(tools))
	for index, tool := range tools {
		if !toolNamePattern.MatchString(tool.Name) {
			return nil, a.requestError(canonical.ErrorInvalidRequest, "invalid_tool_name", "tool name must contain 1-64 letters, digits, underscores, or dashes", fmt.Sprintf("tools[%d].function.name", index))
		}
		parameters := tool.Parameters
		if len(parameters) == 0 {
			parameters = json.RawMessage(`{}`)
		}
		if !validJSONObject(parameters) {
			return nil, a.requestError(canonical.ErrorInvalidRequest, "invalid_tool_schema", "tool parameters must be one JSON object", fmt.Sprintf("tools[%d].function.parameters", index))
		}
		if tool.Strict != nil && !a.policy.capabilities.StrictTools {
			return nil, a.unsupported(fmt.Sprintf("tools[%d].function.strict", index))
		}
		encoded = append(encoded, wireTool{Type: "function", Function: wireToolFunction{
			Name: tool.Name, Description: tool.Description, Parameters: parameters, Strict: tool.Strict,
		}})
	}
	return encoded, nil
}

func validJSONObject(value []byte) bool {
	var object map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(value))
	if err := decoder.Decode(&object); err != nil || object == nil {
		return false
	}
	var trailing any
	return errors.Is(decoder.Decode(&trailing), io.EOF)
}

func (a *openAIAdapter) encodeToolChoice(choice *canonical.ToolChoice, toolCount int) (any, error) {
	if choice == nil {
		return nil, nil
	}
	if toolCount == 0 && choice.Mode != canonical.ToolChoiceNone {
		return nil, a.requestError(canonical.ErrorInvalidRequest, "tool_choice_without_tools", "tool_choice requires tools", "tool_choice")
	}
	switch choice.Mode {
	case canonical.ToolChoiceNone:
		if !a.policy.capabilities.ToolChoiceNone {
			return nil, a.unsupported("tool_choice")
		}
		return string(choice.Mode), nil
	case canonical.ToolChoiceAuto:
		if !a.policy.capabilities.ToolChoiceAuto {
			return nil, a.unsupported("tool_choice")
		}
		return string(choice.Mode), nil
	case canonical.ToolChoiceRequired:
		if !a.policy.capabilities.ToolChoiceRequired {
			return nil, a.unsupported("tool_choice")
		}
		return string(choice.Mode), nil
	case canonical.ToolChoiceFunction:
		if !a.policy.capabilities.ToolChoiceNamed {
			return nil, a.unsupported("tool_choice")
		}
		if !toolNamePattern.MatchString(choice.FunctionName) {
			return nil, a.requestError(canonical.ErrorInvalidRequest, "invalid_tool_choice", "named tool choice requires a valid function name", "tool_choice")
		}
		return map[string]any{"type": "function", "function": map[string]string{"name": choice.FunctionName}}, nil
	default:
		return nil, a.requestError(canonical.ErrorInvalidRequest, "invalid_tool_choice", "tool choice is invalid", "tool_choice")
	}
}
