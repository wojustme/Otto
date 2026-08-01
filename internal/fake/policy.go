package fake

import "github.com/wojustme/otto/internal/agent"

// RequireApprovalPolicy 是开发策略：所有工具调用都必须经过用户审批。
type RequireApprovalPolicy struct{}

// Evaluate 始终返回需要审批，用于稳定演示和测试人工授权链路。
func (RequireApprovalPolicy) Evaluate(agent.ToolCall) agent.PolicyDecision {
	// 开发阶段刻意选择需要人工审批的高摩擦路径，
	// 使真实工具接入前就能清晰观察并测试审批边界。
	return agent.PolicyRequireApproval
}
