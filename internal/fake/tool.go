package fake

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/wojustme/otto/internal/agent"
)

// EchoTool 是无外部副作用的示例工具，用于验证完整工具调用链路。
type EchoTool struct{}

// Definition 返回模型可见的工具描述及严格参数 Schema。
func (EchoTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name:        "echo",
		Description: "Return the provided text as structured JSON.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"],"additionalProperties":false}`),
	}
}

// Execute 校验输入并以结构化 JSON 原样返回 text 字段。
func (EchoTool) Execute(ctx context.Context, arguments json.RawMessage) (agent.ToolResult, error) {
	select {
	// 上游已经取消任务时立即退出，避免继续产生无意义结果。
	case <-ctx.Done():
		return agent.ToolResult{}, ctx.Err()
	// 上下文仍有效时继续解析参数。
	default:
	}
	var input struct {
		Text string `json:"text"`
	}
	// 参数不是期望的 JSON 对象时返回解析错误。
	if err := json.Unmarshal(arguments, &input); err != nil {
		return agent.ToolResult{}, err
	}
	// text 是工具 Schema 中的必填业务字段，空值不执行回显。
	if input.Text == "" {
		return agent.ToolResult{}, errors.New("text is required")
	}
	content, _ := json.Marshal(map[string]string{"echo": input.Text})
	return agent.ToolResult{Content: content}, nil
}
