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
	// ErrRunExists 表示调用方提交了已经存在的 runID。
	ErrRunExists = errors.New("run already exists")
	// ErrRunNotFound 表示目标任务尚未创建或已经无法定位。
	ErrRunNotFound = errors.New("run not found")
	// ErrInvalidState 表示当前任务状态不允许执行请求的操作。
	ErrInvalidState = errors.New("invalid run state")
	// ErrApprovalMismatch 表示审批响应与当前等待的审批不一致。
	ErrApprovalMismatch = errors.New("approval does not match pending request")
)

// EngineOptions 汇总创建 Agent 引擎所需的业务依赖。
type EngineOptions struct {
	// Model 是必需的模型适配器。
	Model Model
	// Tools 是允许模型发现并请求的本地工具集合。
	Tools []Tool
	// Policy 是必需的工具授权策略。
	Policy Policy
	// Clock 和 IDs 可选；留空时使用生产环境默认实现。
	Clock Clock
	IDs   IDGenerator
}

// Engine 管理任务状态、模型回合、工具执行、审批暂停和领域事件。
type Engine struct {
	model       Model
	tools       map[string]Tool
	definitions []ToolDefinition
	policy      Policy
	clock       Clock
	ids         IDGenerator
	// runsMu 只保护运行实例索引。每个运行实例拥有独立的互斥锁，
	// 因此不同任务在调用模型或工具时不会相互阻塞。
	runsMu sync.RWMutex
	runs   map[string]*runState
}

// runStatus 表示单个 Agent 任务在引擎内部的生命周期状态。
type runStatus string

const (
	// runRunning 表示任务可以继续调用模型或执行工具。
	runRunning runStatus = "running"
	// runWaitingApproval 表示任务暂停在工具调用前，等待用户决定。
	runWaitingApproval runStatus = "waiting_approval"
	// runCompleted 表示任务正常完成，不再接受状态变更。
	runCompleted runStatus = "completed"
	// runCancelled 表示任务被主动取消，不再接受状态变更。
	runCancelled runStatus = "cancelled"
	// runFailed 表示任务异常终止，不再接受状态变更。
	runFailed runStatus = "failed"
)

// pendingTool 保存已经由模型提出、但尚未完成授权或执行的工具调用。
type pendingTool struct {
	call       ToolCall
	approvalID string
}

// runState 保存一个任务的完整可变状态，仅供 Engine 内部使用。
type runState struct {
	// 引擎以小型状态机推进单个任务。所有可变对话状态共用一把锁，
	// 同时也能防止同一审批被重复响应。
	mu       sync.Mutex
	id       string
	status   runStatus
	sequence uint64
	turns    int
	messages []Message
	output   strings.Builder
	pending  *pendingTool
}

// NewEngine 校验所有依赖和工具定义，并创建可并发管理多个任务的引擎。
func NewEngine(options EngineOptions) (*Engine, error) {
	// 模型负责产生下一步动作，策略负责守住副作用边界，两者都不可缺少。
	if options.Model == nil || options.Policy == nil {
		return nil, errors.New("model and policy are required")
	}
	// 未注入时间源时使用系统 UTC 时间。
	if options.Clock == nil {
		options.Clock = SystemClock{}
	}
	// 未注入 ID 生成器时使用密码学随机 ID。
	if options.IDs == nil {
		options.IDs = RandomIDGenerator{}
	}

	tools := make(map[string]Tool, len(options.Tools))
	definitions := make([]ToolDefinition, 0, len(options.Tools))
	for _, tool := range options.Tools {
		// nil 工具无法提供定义或执行能力，属于启动配置错误。
		if tool == nil {
			return nil, errors.New("tool cannot be nil")
		}
		definition := tool.Definition()
		// 在启动阶段校验工具定义，避免等模型发起调用后才发现 Schema 非法。
		if strings.TrimSpace(definition.Name) == "" || !json.Valid(definition.InputSchema) {
			return nil, errors.New("tool must have a name and valid JSON schema")
		}
		// 工具名是模型调用和运行时查找的唯一键，重复名称会造成执行歧义。
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

// Start 创建新任务、写入首条用户消息，并立即推进第一个模型回合。
// 返回值是本次同步推进过程中产生的有序事件集合。
func (e *Engine) Start(ctx context.Context, runID, message string) ([]Event, error) {
	// 空 ID 无法关联后续命令，空消息也无法形成有效模型输入。
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(message) == "" {
		return nil, errors.New("run id and message are required")
	}
	run := &runState{id: runID, status: runRunning, messages: []Message{{Role: RoleUser, Content: message}}}
	// 在调用模型前先注册任务，使后续命令可以定位它；同时拒绝调用方意外复用 runID。
	e.runsMu.Lock()
	// 已存在相同 runID 时拒绝覆盖，避免破坏原任务的状态与事件序列。
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

// RespondToApproval 恢复一个正在等待审批的任务，并根据用户决定执行或拒绝工具。
func (e *Engine) RespondToApproval(ctx context.Context, runID, approvalID string, approved bool) ([]Event, error) {
	run, err := e.run(runID)
	// 找不到任务时无法校验审批上下文，直接返回定位错误。
	if err != nil {
		return nil, err
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	// 只有“等待审批”且确实保存了待执行工具时，审批响应才有意义。
	if run.status != runWaitingApproval || run.pending == nil {
		return nil, fmt.Errorf("%w: run is not waiting for approval", ErrInvalidState)
	}
	// approvalID 将界面上的决定绑定到确切的待执行工具调用。
	// 仅有 runID 不够，因为一个任务可能先后请求多个工具。
	if approvalID == "" || approvalID != run.pending.approvalID {
		return nil, fmt.Errorf("%w: %s", ErrApprovalMismatch, approvalID)
	}

	call := run.pending.call
	run.status = runRunning
	events := []Event{e.event(run, EventApprovalResolved, ApprovalResolvedData{
		ApprovalID: approvalID, Approved: approved,
	})}
	// 用户拒绝时不执行工具，而是把结构化拒绝结果写回模型，
	// 让模型有机会解释、调整计划或正常结束任务。
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

// Cancel 将仍在运行或等待审批的任务标记为已取消。
func (e *Engine) Cancel(runID string) ([]Event, error) {
	run, err := e.run(runID)
	// 不存在的任务无法取消。
	if err != nil {
		return nil, err
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	// 已进入终态的任务不可再次取消，避免发出互相矛盾的终态事件。
	if run.status == runCompleted || run.status == runCancelled || run.status == runFailed {
		return nil, fmt.Errorf("%w: run is %s", ErrInvalidState, run.status)
	}
	run.status = runCancelled
	return []Event{e.event(run, EventRunCancelled, nil)}, nil
}

// advance 推进一个完整模型回合，并在允许时继续执行工具和下一回合。
// 调用方必须已经持有 run.mu。
func (e *Engine) advance(ctx context.Context, run *runState) ([]Event, error) {
	// 调用方在整个状态迁移期间持有 run.mu。advance 可以在收到工具结果后递归推进，
	// 但不会让其他命令看到只完成了一半的回合。
	// 只有运行态可以继续调用模型；等待审批和终态都必须停止推进。
	if run.status != runRunning {
		return nil, fmt.Errorf("%w: run is %s", ErrInvalidState, run.status)
	}
	run.turns++
	// 限制模型与工具的自动循环次数，避免异常适配器无限运行。
	if run.turns > 16 {
		return e.fail(run, errors.New("agent exceeded the 16 turn limit")), nil
	}

	turn, err := e.model.Next(ctx, cloneMessages(run.messages), append([]ToolDefinition(nil), e.definitions...))
	// 模型适配器报错属于任务级失败，转换为事件而不是让进程崩溃。
	if err != nil {
		return e.fail(run, fmt.Errorf("model: %w", err)), nil
	}
	var events []Event
	// 有文本时立即累计并发布增量事件，使界面可以流式呈现。
	if turn.Text != "" {
		run.output.WriteString(turn.Text)
		events = append(events, e.event(run, EventModelTextDelta, TextDeltaData{Delta: turn.Text}))
	}

	// 没有工具调用表示模型已经给出最终回答，本任务进入完成态。
	if turn.ToolCall == nil {
		run.messages = append(run.messages, Message{Role: RoleAssistant, Content: turn.Text})
		run.status = runCompleted
		stopReason := turn.StopReason
		// 适配器未提供停止原因时使用稳定默认值，避免协议字段为空。
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
	// 工具调用必须具备可关联的 ID、可查找的名称和合法 JSON 参数。
	if call.ID == "" || call.Name == "" || !json.Valid(call.Arguments) {
		return append(events, e.fail(run, errors.New("model produced an invalid tool call"))...), nil
	}
	_, exists := e.tools[call.Name]
	// 模型只能调用启动时注册的工具，未知名称不能进入执行层。
	if !exists {
		return append(events, e.fail(run, fmt.Errorf("unknown tool %q", call.Name))...), nil
	}
	run.messages = append(run.messages, Message{Role: RoleAssistant, Content: turn.Text, ToolCall: &call})
	run.pending = &pendingTool{call: call}
	events = append(events, e.event(run, EventToolRequested, ToolRequestedData{Call: call}))

	// 授权判断刻意放在 Tool.Execute 之外：同一个工具可以按策略在本地放行、
	// 在远程拒绝，或暂停并等待用户审批。
	switch e.policy.Evaluate(call) {
	case PolicyRequireApproval:
		// 需要审批时生成一次性 approvalID，并暂停在等待态。
		approvalID, idErr := e.ids.NewID("approval")
		// 无法生成审批 ID 时不能安全建立授权关联，因此任务失败。
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
		// 策略拒绝不会触发工具副作用，但会把拒绝结果回填给模型继续推理。
		const reason = "denied by policy"
		events = append(events, e.event(run, EventToolDenied, ToolDeniedData{
			CallID: call.ID, Name: call.Name, Reason: reason,
		}))
		e.appendToolMessage(run, call, errorJSON(reason))
		run.pending = nil
		next, advanceErr := e.advance(ctx, run)
		return append(events, next...), advanceErr

	case PolicyAllow:
		// 策略放行后立即执行工具，并使用结果继续下一模型回合。
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
		// 未知策略值说明 Policy 实现违反接口约定，任务必须失败。
		return append(events, e.fail(run, errors.New("policy returned an invalid decision"))...), nil
	}
}

// executeTool 调用已注册工具，并把执行异常或非法结果规范化为 ToolResult。
func (e *Engine) executeTool(ctx context.Context, call ToolCall) ToolResult {
	result, err := e.tools[call.Name].Execute(ctx, append(json.RawMessage(nil), call.Arguments...))
	// 工具返回 Go error 时，将错误包装成模型和协议都能消费的 JSON。
	if err != nil {
		return ToolResult{Content: errorJSON(err.Error()), IsError: true}
	}
	// 工具结果会重新进入模型上下文并跨越进程边界，
	// 因此非法 JSON 会在边界处转换成结构化工具错误。
	if !json.Valid(result.Content) {
		return ToolResult{Content: errorJSON("tool returned invalid JSON"), IsError: true}
	}
	return result
}

// fail 把任务切换到失败终态，并生成对应的失败事件。
func (e *Engine) fail(run *runState, cause error) []Event {
	run.status = runFailed
	return []Event{e.event(run, EventRunFailed, RunFailedData{Message: cause.Error()})}
}

// run 从并发安全的任务索引中查找指定运行实例。
func (e *Engine) run(runID string) (*runState, error) {
	e.runsMu.RLock()
	run, exists := e.runs[runID]
	e.runsMu.RUnlock()
	// 索引中不存在 runID 时向调用方返回可识别的领域错误。
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrRunNotFound, runID)
	}
	return run, nil
}

// event 为任务生成下一条带单调序号和 UTC 时间的领域事件。
func (e *Engine) event(run *runState, eventType EventType, data any) Event {
	// Sequence 在单个任务内单调递增，即使未来传输或渲染改为异步，
	// 界面仍能据此还原事件的因果顺序。
	run.sequence++
	return Event{Type: eventType, RunID: run.id, Sequence: run.sequence, OccurredAt: e.clock.Now().UTC(), Data: data}
}

// appendToolMessage 把工具结果追加到模型上下文，并保留与原调用的关联信息。
func (e *Engine) appendToolMessage(run *runState, call ToolCall, content json.RawMessage) {
	run.messages = append(run.messages, Message{
		Role: RoleTool, Content: string(content), ToolCallID: call.ID, ToolName: call.Name,
	})
}

// cloneMessages 复制消息切片，避免模型适配器意外修改引擎持有的切片结构。
func cloneMessages(messages []Message) []Message {
	result := make([]Message, len(messages))
	copy(result, messages)
	return result
}

// errorJSON 把普通错误文本转换成统一的结构化 JSON 结果。
func errorJSON(message string) json.RawMessage {
	value, _ := json.Marshal(map[string]string{"error": message})
	return value
}
