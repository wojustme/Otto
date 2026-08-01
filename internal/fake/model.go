// Package fake contains deterministic adapters for development and tests.
package fake

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/wojustme/otto/internal/agent"
)

type Model struct{}

func (Model) Next(_ context.Context, messages []agent.Message, _ []agent.ToolDefinition) (agent.Turn, error) {
	if len(messages) == 0 {
		return agent.Turn{}, errors.New("a message is required")
	}
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == agent.RoleTool {
			return agent.Turn{
				Text:       "工具执行完成，结果是 " + messages[index].Content,
				StopReason: "completed",
			}, nil
		}
	}

	prompt := ""
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == agent.RoleUser {
			prompt = messages[index].Content
			break
		}
	}
	arguments, _ := json.Marshal(map[string]string{"text": prompt})
	return agent.Turn{
		Text:     "我准备调用本地 echo 工具确认任务内容。",
		ToolCall: &agent.ToolCall{ID: "call-echo-1", Name: "echo", Arguments: arguments},
	}, nil
}
