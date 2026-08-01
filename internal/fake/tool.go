package fake

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/wojustme/otto/internal/agent"
)

type EchoTool struct{}

func (EchoTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "echo",
		Description: "Return the provided text as structured JSON.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`),
	}
}

func (EchoTool) Execute(ctx context.Context, arguments json.RawMessage) (agent.ToolResult, error) {
	select {
	case <-ctx.Done():
		return agent.ToolResult{}, ctx.Err()
	default:
	}
	var input struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(arguments, &input); err != nil {
		return agent.ToolResult{}, err
	}
	if input.Text == "" {
		return agent.ToolResult{}, errors.New("text is required")
	}
	content, _ := json.Marshal(map[string]string{"echo": input.Text})
	return agent.ToolResult{Content: content}, nil
}
