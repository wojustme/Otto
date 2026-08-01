// Package agent implements Otto's private, transport-independent agent engine.
package agent

import (
	"context"
	"encoding/json"
	"time"
)

type EventType string

const (
	EventRunStarted             EventType = "run.started"
	EventModelTextDelta         EventType = "model.text.delta"
	EventModelResponseCompleted EventType = "model.response.completed"
	EventToolRequested          EventType = "tool.requested"
	EventApprovalRequested      EventType = "approval.requested"
	EventApprovalResolved       EventType = "approval.resolved"
	EventToolStarted            EventType = "tool.started"
	EventToolCompleted          EventType = "tool.completed"
	EventToolDenied             EventType = "tool.denied"
	EventRunCompleted           EventType = "run.completed"
	EventRunCancelled           EventType = "run.cancelled"
	EventRunFailed              EventType = "run.failed"
)

type Event struct {
	Type       EventType
	RunID      string
	Sequence   uint64
	OccurredAt time.Time
	Data       any
}

type RunStartedData struct {
	Message string `json:"message"`
}

type TextDeltaData struct {
	Delta string `json:"delta"`
}

type ResponseCompletedData struct {
	StopReason string `json:"stopReason"`
}

type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ToolRequestedData struct {
	Call ToolCall `json:"call"`
}

type ApprovalRequestedData struct {
	ApprovalID string   `json:"approvalId"`
	Call       ToolCall `json:"call"`
	Summary    string   `json:"summary"`
}

type ApprovalResolvedData struct {
	ApprovalID string `json:"approvalId"`
	Approved   bool   `json:"approved"`
}

type ToolStartedData struct {
	Call ToolCall `json:"call"`
}

type ToolCompletedData struct {
	CallID  string          `json:"callId"`
	Name    string          `json:"name"`
	Content json.RawMessage `json:"content"`
	IsError bool            `json:"isError"`
}

type ToolDeniedData struct {
	CallID string `json:"callId"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type RunCompletedData struct {
	Output string `json:"output"`
}

type RunFailedData struct {
	Message string `json:"message"`
}

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role       Role
	Content    string
	ToolCall   *ToolCall
	ToolCallID string
	ToolName   string
}

type ToolDefinition struct {
	Name        string
	Description string
	InputSchema json.RawMessage
}

type ToolResult struct {
	Content json.RawMessage
	IsError bool
}

type Tool interface {
	Definition() ToolDefinition
	Execute(context.Context, json.RawMessage) (ToolResult, error)
}

type Turn struct {
	Text       string
	ToolCall   *ToolCall
	StopReason string
}

type Model interface {
	Next(context.Context, []Message, []ToolDefinition) (Turn, error)
}

type PolicyDecision string

const (
	PolicyAllow           PolicyDecision = "allow"
	PolicyDeny            PolicyDecision = "deny"
	PolicyRequireApproval PolicyDecision = "require_approval"
)

type Policy interface {
	Evaluate(ToolCall) PolicyDecision
}

type Clock interface {
	Now() time.Time
}

type IDGenerator interface {
	NewID(prefix string) (string, error)
}
