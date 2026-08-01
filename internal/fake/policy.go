package fake

import "github.com/wojustme/otto/internal/agent"

type RequireApprovalPolicy struct{}

func (RequireApprovalPolicy) Evaluate(agent.ToolCall) agent.PolicyDecision {
	return agent.PolicyRequireApproval
}
