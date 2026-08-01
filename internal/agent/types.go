// Package agent 实现 Otto 私有且与传输方式无关的 Agent 引擎。
package agent

import (
	"context"
	"encoding/json"
	"time"
)

// EventType 表示 Agent 引擎对外发布的领域事件类型。
// 字符串值会进入跨进程协议，因此修改时需要同步考虑协议兼容性。
type EventType string

const (
	// EventRunStarted 表示引擎已经接受用户任务并创建运行实例。
	EventRunStarted EventType = "run.started"
	// EventModelTextDelta 表示模型产生了一段可追加到界面的文本。
	EventModelTextDelta EventType = "model.text.delta"
	// EventModelResponseCompleted 表示当前模型响应已经结束。
	EventModelResponseCompleted EventType = "model.response.completed"
	// EventToolRequested 表示模型请求调用某个本地工具。
	EventToolRequested EventType = "tool.requested"
	// EventApprovalRequested 表示工具调用暂停，正在等待用户授权。
	EventApprovalRequested EventType = "approval.requested"
	// EventApprovalResolved 表示用户已经同意或拒绝本次审批。
	EventApprovalResolved EventType = "approval.resolved"
	// EventToolStarted 表示工具已经通过策略检查并开始执行。
	EventToolStarted EventType = "tool.started"
	// EventToolCompleted 表示工具执行结束，结果可能成功也可能是结构化错误。
	EventToolCompleted EventType = "tool.completed"
	// EventToolDenied 表示工具被用户或策略拒绝，没有产生本地副作用。
	EventToolDenied EventType = "tool.denied"
	// EventRunCompleted 表示整个 Agent 任务正常完成。
	EventRunCompleted EventType = "run.completed"
	// EventRunCancelled 表示任务被调用方主动取消。
	EventRunCancelled EventType = "run.cancelled"
	// EventRunFailed 表示任务因为模型、协议或状态错误而失败。
	EventRunFailed EventType = "run.failed"
)

// Event 是引擎发布的传输无关事件。
type Event struct {
	// Event 刻意不包含 protocol 或 Wails 类型，使不同传输适配器可以把同一组
	// 引擎事件序列化到桌面端、移动端、测试或持久化存储。
	Type       EventType
	RunID      string
	Sequence   uint64
	OccurredAt time.Time
	Data       any
}

// RunStartedData 保存任务开始事件中的原始用户消息。
type RunStartedData struct {
	Message string `json:"message"`
}

// TextDeltaData 保存本次模型输出的增量文本。
type TextDeltaData struct {
	Delta string `json:"delta"`
}

// ResponseCompletedData 描述模型停止生成的原因。
type ResponseCompletedData struct {
	StopReason string `json:"stopReason"`
}

// ToolCall 是模型提出的一次结构化工具调用。
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ToolRequestedData 把模型工具调用附加到请求事件中。
type ToolRequestedData struct {
	Call ToolCall `json:"call"`
}

// ApprovalRequestedData 提供界面呈现审批所需的信息。
type ApprovalRequestedData struct {
	ApprovalID string   `json:"approvalId"`
	Call       ToolCall `json:"call"`
	Summary    string   `json:"summary"`
}

// ApprovalResolvedData 记录用户对审批的最终决定。
type ApprovalResolvedData struct {
	ApprovalID string `json:"approvalId"`
	Approved   bool   `json:"approved"`
}

// ToolStartedData 标识即将执行的工具调用。
type ToolStartedData struct {
	Call ToolCall `json:"call"`
}

// ToolCompletedData 保存工具返回的 JSON 内容及错误标识。
type ToolCompletedData struct {
	CallID  string          `json:"callId"`
	Name    string          `json:"name"`
	Content json.RawMessage `json:"content"`
	IsError bool            `json:"isError"`
}

// ToolDeniedData 说明哪个工具调用被拒绝以及拒绝原因。
type ToolDeniedData struct {
	CallID string `json:"callId"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// RunCompletedData 保存本次任务累积得到的最终文本输出。
type RunCompletedData struct {
	Output string `json:"output"`
}

// RunFailedData 保存可以展示给调用方的失败信息。
type RunFailedData struct {
	Message string `json:"message"`
}

// Role 表示一条模型上下文消息的发送方角色。
type Role string

const (
	// RoleUser 表示用户输入。
	RoleUser Role = "user"
	// RoleAssistant 表示模型回复或模型发起的工具调用。
	RoleAssistant Role = "assistant"
	// RoleTool 表示工具执行后回填给模型的结果。
	RoleTool Role = "tool"
)

// Message 是传给模型适配器的统一上下文消息。
type Message struct {
	Role       Role
	Content    string
	ToolCall   *ToolCall
	ToolCallID string
	ToolName   string
}

// ToolDefinition 描述模型可见的工具名称、用途和参数 Schema。
type ToolDefinition struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

// ToolResult 是工具执行后的结构化 JSON 结果。
type ToolResult struct {
	Content json.RawMessage
	IsError bool
}

// Tool 定义所有本地能力必须实现的描述与执行接口。
type Tool interface {
	// Definition 会暴露给模型；只有 Policy 完成授权判断后才会调用 Execute。
	Definition() ToolDefinition
	Execute(context.Context, json.RawMessage) (ToolResult, error)
}

// Turn 表示模型单次推理的结果：可以输出文本、请求工具或直接结束。
type Turn struct {
	Text       string
	ToolCall   *ToolCall
	StopReason string
}

// Model 抽象具体模型 Provider，使引擎不依赖任意模型 SDK。
type Model interface {
	// Next 只生成一个模型回合。循环、工具执行、审批、限制和事件发送
	// 仍由 Engine 以确定性方式负责。
	Next(context.Context, []Message, []ToolDefinition) (Turn, error)
}

// PolicyDecision 表示策略层对一次工具调用的授权结论。
type PolicyDecision string

const (
	// PolicyAllow 表示无需用户确认，可以立即执行工具。
	PolicyAllow PolicyDecision = "allow"
	// PolicyDeny 表示策略直接拒绝工具调用。
	PolicyDeny PolicyDecision = "deny"
	// PolicyRequireApproval 表示暂停任务并等待用户明确授权。
	PolicyRequireApproval PolicyDecision = "require_approval"
)

// Policy 决定模型提出的工具调用能否产生本地副作用。
type Policy interface {
	// Policy 是模型意图与本地副作用之间的安全边界。
	Evaluate(ToolCall) PolicyDecision
}

// Clock 提供可替换时间源，便于测试稳定验证事件时间。
type Clock interface {
	Now() time.Time
}

// IDGenerator 为审批等内部对象生成唯一标识，测试可以注入固定实现。
type IDGenerator interface {
	NewID(prefix string) (string, error)
}
