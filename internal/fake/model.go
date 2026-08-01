// Package fake 提供用于开发与测试的确定性依赖适配器。
package fake

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/wojustme/otto/internal/agent"
)

// Model 是用于本地开发的确定性模型适配器，不访问任何外部模型服务。
type Model struct{}

// Next 按固定的双回合流程先请求 echo 工具，再根据工具结果生成最终回复。
func (Model) Next(_ context.Context, messages []agent.Message, _ []agent.ToolDefinition) (agent.Turn, error) {
	// 没有上下文说明调用方违反 Model 接口约定，无法生成有效回合。
	if len(messages) == 0 {
		return agent.Turn{}, errors.New("a message is required")
	}
	// 收到工具结果后即可结束这个确定性的双回合场景；否则首回合始终请求 echo，
	// 从而保证审批测试稳定可复现。
	for index := len(messages) - 1; index >= 0; index-- {
		// 找到最近的工具结果时，说明 echo 已执行，可以生成最终回答。
		if messages[index].Role == agent.RoleTool {
			return agent.Turn{
				Text:       "工具执行完成，结果是 " + messages[index].Content,
				StopReason: "completed",
			}, nil
		}
	}

	prompt := ""
	for index := len(messages) - 1; index >= 0; index-- {
		// 从后向前选择最近一条用户消息作为 echo 参数。
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
