package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

var (
	ErrRunExists        = errors.New("run already exists")
	ErrRunNotFound      = errors.New("run not found")
	ErrInvalidState     = errors.New("invalid run state")
	ErrApprovalMismatch = errors.New("approval does not match pending request")
)

type EngineOptions struct {
	Model  Model
	Tools  []Tool
	Policy Policy
	Clock  Clock
	IDs    IDGenerator
}

type Engine struct {
	model       Model
	tools       map[string]Tool
	definitions []ToolDefinition
	policy      Policy
	clock       Clock
	ids         IDGenerator
	runsMu      sync.RWMutex
	runs        map[string]*runState
}

type runStatus string

const (
	runRunning         runStatus = "running"
	runWaitingApproval runStatus = "waiting_approval"
	runCompleted       runStatus = "completed"
	runCancelled       runStatus = "cancelled"
	runFailed          runStatus = "failed"
)

type pendingTool struct {
	call       ToolCall
	approvalID string
}

type runState struct {
	mu       sync.Mutex
	id       string
	status   runStatus
	sequence uint64
	turns    int
	messages []Message
	output   strings.Builder
	pending  *pendingTool
}

func NewEngine(options EngineOptions) (*Engine, error) {
	if options.Model == nil || options.Policy == nil {
		return nil, errors.New("model and policy are required")
	}
	if options.Clock == nil {
		options.Clock = SystemClock{}
	}
	if options.IDs == nil {
		options.IDs = RandomIDGenerator{}
	}

	tools := make(map[string]Tool, len(options.Tools))
	definitions := make([]ToolDefinition, 0, len(options.Tools))
	for _, tool := range options.Tools {
		if tool == nil {
			return nil, errors.New("tool cannot be nil")
		}
		definition := tool.Definition()
		if strings.TrimSpace(definition.Name) == "" || !json.Valid(definition.InputSchema) {
			return nil, errors.New("tool must have a name and valid JSON schema")
		}
		if _, exists := tools[definition.Name]; exists {
			return nil, fmt.Errorf("duplicate tool %q", definition.Name)
		}
		tools[definition.Name] = tool
		definitions = append(definitions, definition)
	}

	return &Engine{
		model: options.Model, tools: tools, definitions: definitions,
		policy: options.Policy, clock: options.Clock, ids: options.IDs,
		runs: make(map[string]*runState),
	}, nil
}

func (e *Engine) Start(ctx context.Context, runID, message string) ([]Event, error) {
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(message) == "" {
		return nil, errors.New("run id and message are required")
	}
	run := &runState{id: runID, status: runRunning, messages: []Message{{Role: RoleUser, Content: message}}}
	e.runsMu.Lock()
	if _, exists := e.runs[runID]; exists {
		e.runsMu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrRunExists, runID)
	}
	e.runs[runID] = run
	e.runsMu.Unlock()

	run.mu.Lock()
	defer run.mu.Unlock()
	events := []Event{e.event(run, EventRunStarted, RunStartedData{Message: message})}
	next, err := e.advance(ctx, run)
	return append(events, next...), err
}

func (e *Engine) RespondToApproval(ctx context.Context, runID, approvalID string, approved bool) ([]Event, error) {
	run, err := e.run(runID)
	if err != nil {
		return nil, err
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	if run.status != runWaitingApproval || run.pending == nil {
		return nil, fmt.Errorf("%w: run is not waiting for approval", ErrInvalidState)
	}
	if approvalID == "" || approvalID != run.pending.approvalID {
		return nil, fmt.Errorf("%w: %s", ErrApprovalMismatch, approvalID)
	}

	call := run.pending.call
	run.status = runRunning
	events := []Event{e.event(run, EventApprovalResolved, ApprovalResolvedData{
		ApprovalID: approvalID, Approved: approved,
	})}
	if !approved {
		const reason = "denied by user"
		events = append(events, e.event(run, EventToolDenied, ToolDeniedData{
			CallID: call.ID, Name: call.Name, Reason: reason,
		}))
		e.appendToolMessage(run, call, errorJSON(reason))
		run.pending = nil
		next, advanceErr := e.advance(ctx, run)
		return append(events, next...), advanceErr
	}

	events = append(events, e.event(run, EventToolStarted, ToolStartedData{Call: call}))
	result := e.executeTool(ctx, call)
	events = append(events, e.event(run, EventToolCompleted, ToolCompletedData{
		CallID: call.ID, Name: call.Name, Content: result.Content, IsError: result.IsError,
	}))
	e.appendToolMessage(run, call, result.Content)
	run.pending = nil
	next, advanceErr := e.advance(ctx, run)
	return append(events, next...), advanceErr
}

func (e *Engine) Cancel(runID string) ([]Event, error) {
	run, err := e.run(runID)
	if err != nil {
		return nil, err
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	if run.status == runCompleted || run.status == runCancelled || run.status == runFailed {
		return nil, fmt.Errorf("%w: run is %s", ErrInvalidState, run.status)
	}
	run.status = runCancelled
	return []Event{e.event(run, EventRunCancelled, nil)}, nil
}

func (e *Engine) advance(ctx context.Context, run *runState) ([]Event, error) {
	if run.status != runRunning {
		return nil, fmt.Errorf("%w: run is %s", ErrInvalidState, run.status)
	}
	run.turns++
	if run.turns > 16 {
		return e.fail(run, errors.New("agent exceeded the 16 turn limit")), nil
	}

	turn, err := e.model.Next(ctx, cloneMessages(run.messages), append([]ToolDefinition(nil), e.definitions...))
	if err != nil {
		return e.fail(run, fmt.Errorf("model: %w", err)), nil
	}
	var events []Event
	if turn.Text != "" {
		run.output.WriteString(turn.Text)
		events = append(events, e.event(run, EventModelTextDelta, TextDeltaData{Delta: turn.Text}))
	}

	if turn.ToolCall == nil {
		run.messages = append(run.messages, Message{Role: RoleAssistant, Content: turn.Text})
		run.status = runCompleted
		stopReason := turn.StopReason
		if stopReason == "" {
			stopReason = "completed"
		}
		events = append(events,
			e.event(run, EventModelResponseCompleted, ResponseCompletedData{StopReason: stopReason}),
			e.event(run, EventRunCompleted, RunCompletedData{Output: run.output.String()}),
		)
		return events, nil
	}

	call := *turn.ToolCall
	if call.ID == "" || call.Name == "" || !json.Valid(call.Arguments) {
		return append(events, e.fail(run, errors.New("model produced an invalid tool call"))...), nil
	}
	_, exists := e.tools[call.Name]
	if !exists {
		return append(events, e.fail(run, fmt.Errorf("unknown tool %q", call.Name))...), nil
	}
	run.messages = append(run.messages, Message{Role: RoleAssistant, Content: turn.Text, ToolCall: &call})
	run.pending = &pendingTool{call: call}
	events = append(events, e.event(run, EventToolRequested, ToolRequestedData{Call: call}))

	switch e.policy.Evaluate(call) {
	case PolicyRequireApproval:
		approvalID, idErr := e.ids.NewID("approval")
		if idErr != nil {
			return append(events, e.fail(run, idErr)...), nil
		}
		run.pending.approvalID = approvalID
		run.status = runWaitingApproval
		events = append(events, e.event(run, EventApprovalRequested, ApprovalRequestedData{
			ApprovalID: approvalID, Call: call, Summary: "Allow Otto to run " + call.Name,
		}))
		return events, nil

	case PolicyDeny:
		const reason = "denied by policy"
		events = append(events, e.event(run, EventToolDenied, ToolDeniedData{
			CallID: call.ID, Name: call.Name, Reason: reason,
		}))
		e.appendToolMessage(run, call, errorJSON(reason))
		run.pending = nil
		next, advanceErr := e.advance(ctx, run)
		return append(events, next...), advanceErr

	case PolicyAllow:
		events = append(events, e.event(run, EventToolStarted, ToolStartedData{Call: call}))
		result := e.executeTool(ctx, call)
		events = append(events, e.event(run, EventToolCompleted, ToolCompletedData{
			CallID: call.ID, Name: call.Name, Content: result.Content, IsError: result.IsError,
		}))
		e.appendToolMessage(run, call, result.Content)
		run.pending = nil
		next, advanceErr := e.advance(ctx, run)
		return append(events, next...), advanceErr

	default:
		return append(events, e.fail(run, errors.New("policy returned an invalid decision"))...), nil
	}
}

func (e *Engine) executeTool(ctx context.Context, call ToolCall) ToolResult {
	result, err := e.tools[call.Name].Execute(ctx, append(json.RawMessage(nil), call.Arguments...))
	if err != nil {
		return ToolResult{Content: errorJSON(err.Error()), IsError: true}
	}
	if !json.Valid(result.Content) {
		return ToolResult{Content: errorJSON("tool returned invalid JSON"), IsError: true}
	}
	return result
}

func (e *Engine) fail(run *runState, cause error) []Event {
	run.status = runFailed
	return []Event{e.event(run, EventRunFailed, RunFailedData{Message: cause.Error()})}
}

func (e *Engine) run(runID string) (*runState, error) {
	e.runsMu.RLock()
	run, exists := e.runs[runID]
	e.runsMu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}
	return run, nil
}

func (e *Engine) event(run *runState, eventType EventType, data any) Event {
	run.sequence++
	return Event{Type: eventType, RunID: run.id, Sequence: run.sequence, OccurredAt: e.clock.Now().UTC(), Data: data}
}

func (e *Engine) appendToolMessage(run *runState, call ToolCall, content json.RawMessage) {
	run.messages = append(run.messages, Message{
		Role: RoleTool, Content: string(content), ToolCallID: call.ID, ToolName: call.Name,
	})
}

func cloneMessages(messages []Message) []Message {
	result := make([]Message, len(messages))
	copy(result, messages)
	return result
}

func errorJSON(message string) json.RawMessage {
	value, _ := json.Marshal(map[string]string{"error": message})
	return value
}
