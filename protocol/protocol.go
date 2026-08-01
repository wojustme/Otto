// Package protocol 定义 Otto 各进程共享且带版本约束的通信边界。
package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Version 是桌面壳与 agentd 之间的兼容性边界。
// 当任意一端无法再安全理解另一端时，需要递增此版本号。
const Version = 1

const (
	// TypeRuntimeReady 表示 sidecar 已完成初始化，可以接收命令。
	TypeRuntimeReady = "runtime.ready"
	// TypeRuntimePing 是桌面端发起的存活探测命令。
	TypeRuntimePing = "runtime.ping"
	// TypeRuntimePong 是 Runtime 对存活探测的响应事件。
	TypeRuntimePong = "runtime.pong"
	// TypeRuntimeError 表示命令格式、版本或领域操作出现错误。
	TypeRuntimeError = "runtime.error"
	// TypeRunStart 表示创建并启动一个新的 Agent 任务。
	TypeRunStart = "run.start"
	// TypeRunCancel 表示取消一个尚未进入终态的任务。
	TypeRunCancel = "run.cancel"
	// TypeApprovalRespond 表示用户对待审批工具调用作出决定。
	TypeApprovalRespond = "approval.respond"
)

var (
	// ErrInvalidMessage 表示消息不符合当前协议结构或字段约束。
	ErrInvalidMessage = errors.New("invalid protocol message")
	// ErrUnsupportedVersion 表示消息版本与当前进程不兼容。
	ErrUnsupportedVersion = errors.New("unsupported protocol version")
	// ErrUnsupportedCommand 表示消息类型不是当前 Runtime 支持的命令。
	ErrUnsupportedCommand = errors.New("unsupported protocol command")
)

// CommandEnvelope 是桌面端写入 agentd stdin 的统一命令信封。
type CommandEnvelope struct {
	// RequestID 用于关联命令及其即时错误或事件；RunID 在完整 Agent 任务期间保持稳定，
	// 可以跨越多条命令。
	ProtocolVersion int    `json:"protocolVersion"`
	Type            string `json:"type"`
	RequestID       string `json:"requestId"`
	RunID           string `json:"runId,omitempty"`
	Data            any    `json:"data,omitempty"`
}

// Event 是 agentd 写入 stdout 的统一事件信封。
type Event struct {
	// Sequence 表示单个任务内的事件顺序；RequestID 标识触发当前事件批次的命令。
	ProtocolVersion int    `json:"protocolVersion"`
	Type            string `json:"type"`
	RequestID       string `json:"requestId,omitempty"`
	RunID           string `json:"runId,omitempty"`
	Sequence        uint64 `json:"sequence,omitempty"`
	OccurredAt      string `json:"occurredAt,omitempty"`
	Data            any    `json:"data,omitempty"`
}

// Command 是严格解码后的命令联合类型。
type Command interface {
	// CommandType 返回命令的协议类型字符串。
	CommandType() string
	// CommandRequestID 返回用于关联响应的请求 ID。
	CommandRequestID() string
}

// RuntimePing 携带桌面端发起存活探测的时间。
type RuntimePing struct {
	RequestID string
	SentAt    time.Time
}

// CommandType 标识该值为 runtime.ping 命令。
func (c RuntimePing) CommandType() string { return TypeRuntimePing }

// CommandRequestID 返回本次存活探测的请求 ID。
func (c RuntimePing) CommandRequestID() string { return c.RequestID }

// RunStart 描述启动 Agent 任务所需的 ID 和用户消息。
type RunStart struct {
	RequestID string
	RunID     string
	Message   string
}

// CommandType 标识该值为 run.start 命令。
func (c RunStart) CommandType() string { return TypeRunStart }

// CommandRequestID 返回启动命令的请求 ID。
func (c RunStart) CommandRequestID() string { return c.RequestID }

// ApprovalRespond 描述用户对指定审批请求的决定。
type ApprovalRespond struct {
	RequestID  string
	RunID      string
	ApprovalID string
	Approved   bool
}

// CommandType 标识该值为 approval.respond 命令。
func (c ApprovalRespond) CommandType() string { return TypeApprovalRespond }

// CommandRequestID 返回审批响应命令的请求 ID。
func (c ApprovalRespond) CommandRequestID() string { return c.RequestID }

// RunCancel 描述需要取消的任务及本次命令 ID。
type RunCancel struct {
	RequestID string
	RunID     string
}

// CommandType 标识该值为 run.cancel 命令。
func (c RunCancel) CommandType() string { return TypeRunCancel }

// CommandRequestID 返回取消命令的请求 ID。
func (c RunCancel) CommandRequestID() string { return c.RequestID }

// commandHeader 是第一阶段解码使用的内部信封结构，Data 暂时保留原始 JSON。
type commandHeader struct {
	ProtocolVersion int             `json:"protocolVersion"`
	Type            string          `json:"type"`
	RequestID       string          `json:"requestId"`
	RunID           string          `json:"runId,omitempty"`
	Data            json.RawMessage `json:"data,omitempty"`
}

// DecodeCommand 严格解析一行 NDJSON，并根据 type 返回具体命令类型。
func DecodeCommand(line []byte) (Command, error) {
	// 先解析信封，再解析命令专属数据。两次解析都采用严格模式，
	// 使协议漂移明确报错，而不是静默丢弃字段。
	var header commandHeader
	// 信封本身不是单一合法 JSON 对象时，整条消息都不可处理。
	if err := decodeStrict(line, &header); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	// 版本不一致时拒绝猜测兼容性，要求调用方显式升级或降级。
	if header.ProtocolVersion != Version {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrUnsupportedVersion, header.ProtocolVersion, Version)
	}
	// 所有命令都必须有 requestId，错误和事件才能回到正确调用方。
	if strings.TrimSpace(header.RequestID) == "" {
		return nil, fmt.Errorf("%w: requestId is required", ErrInvalidMessage)
	}

	switch header.Type {
	case TypeRuntimePing:
		// 存活探测必须携带 RFC3339 时间，便于调用方计算往返延迟。
		var data struct {
			SentAt string `json:"sentAt"`
		}
		// data 缺失或包含未知字段时视为无效 Ping。
		if err := decodeData(header.Data, &data); err != nil {
			return nil, err
		}
		sentAt, err := time.Parse(time.RFC3339Nano, data.SentAt)
		// sentAt 无法解析时不能计算可靠的延迟信息。
		if err != nil {
			return nil, fmt.Errorf("%w: data.sentAt must be RFC3339", ErrInvalidMessage)
		}
		return RuntimePing{RequestID: header.RequestID, SentAt: sentAt}, nil

	case TypeRunStart:
		// 新任务必须提供稳定 runId，后续审批和取消都依赖它。
		if strings.TrimSpace(header.RunID) == "" {
			return nil, fmt.Errorf("%w: runId is required", ErrInvalidMessage)
		}
		var data struct {
			Message string `json:"message"`
		}
		// 任务 data 必须严格匹配 message 结构。
		if err := decodeData(header.Data, &data); err != nil {
			return nil, err
		}
		// 空白消息无法形成有效 Agent 输入。
		if strings.TrimSpace(data.Message) == "" {
			return nil, fmt.Errorf("%w: data.message is required", ErrInvalidMessage)
		}
		return RunStart{RequestID: header.RequestID, RunID: header.RunID, Message: data.Message}, nil

	case TypeApprovalRespond:
		// 审批必须绑定现有任务，缺少 runId 时无法定位待审批状态。
		if strings.TrimSpace(header.RunID) == "" {
			return nil, fmt.Errorf("%w: runId is required", ErrInvalidMessage)
		}
		var data struct {
			ApprovalID string `json:"approvalId"`
			Approved   *bool  `json:"approved"`
		}
		// 审批 data 必须严格包含 approvalId 和 approved。
		if err := decodeData(header.Data, &data); err != nil {
			return nil, err
		}
		// approved 使用指针是为了区分“明确拒绝 false”和“字段缺失”。
		if data.ApprovalID == "" || data.Approved == nil {
			return nil, fmt.Errorf("%w: approvalId and approved are required", ErrInvalidMessage)
		}
		return ApprovalRespond{
			RequestID: header.RequestID, RunID: header.RunID,
			ApprovalID: data.ApprovalID, Approved: *data.Approved,
		}, nil

	case TypeRunCancel:
		// 取消命令只有在提供目标 runId 时才有意义。
		if strings.TrimSpace(header.RunID) == "" {
			return nil, fmt.Errorf("%w: runId is required", ErrInvalidMessage)
		}
		return RunCancel{RequestID: header.RequestID, RunID: header.RunID}, nil

	default:
		// 未知 type 不做前向猜测，返回可识别的不支持命令错误。
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedCommand, header.Type)
	}
}

// NewRunStart 构造符合当前协议版本的任务启动命令。
func NewRunStart(requestID, runID, message string) CommandEnvelope {
	return CommandEnvelope{ProtocolVersion: Version, Type: TypeRunStart, RequestID: requestID, RunID: runID,
		Data: map[string]any{"message": message}}
}

// NewApprovalResponse 构造用户审批响应命令。
func NewApprovalResponse(requestID, runID, approvalID string, approved bool) CommandEnvelope {
	return CommandEnvelope{ProtocolVersion: Version, Type: TypeApprovalRespond, RequestID: requestID, RunID: runID,
		Data: map[string]any{"approvalId": approvalID, "approved": approved}}
}

// NewRunCancel 构造任务取消命令。
func NewRunCancel(requestID, runID string) CommandEnvelope {
	return CommandEnvelope{ProtocolVersion: Version, Type: TypeRunCancel, RequestID: requestID, RunID: runID}
}

// NewReady 构造 agentd 启动完成后的握手事件。
func NewReady(runtimeVersion string) Event {
	return Event{ProtocolVersion: Version, Type: TypeRuntimeReady,
		Data: map[string]any{"runtimeVersion": runtimeVersion}}
}

// NewPong 构造存活探测响应，同时返回发送与接收时间。
func NewPong(requestID string, sentAt, receivedAt time.Time) Event {
	return Event{ProtocolVersion: Version, Type: TypeRuntimePong, RequestID: requestID,
		Data: map[string]any{
			"sentAt":     sentAt.UTC().Format(time.RFC3339Nano),
			"receivedAt": receivedAt.UTC().Format(time.RFC3339Nano),
		}}
}

// NewError 构造可与原命令关联的结构化 Runtime 错误事件。
func NewError(requestID, code, message string) Event {
	return Event{ProtocolVersion: Version, Type: TypeRuntimeError, RequestID: requestID,
		Data: map[string]any{"code": code, "message": message}}
}

// PeekRequestID 从可能已经损坏的消息中尽力提取 requestId。
func PeekRequestID(line []byte) string {
	// 即使输入格式错误，只要还能读取 requestId，也应返回可关联的错误，
	// 因此这里刻意使用宽松的尽力解析。
	var value struct {
		RequestID string `json:"requestId"`
	}
	_ = json.Unmarshal(line, &value)
	return value.RequestID
}

// decodeData 校验 data 必须存在，并严格解码到命令专属结构。
func decodeData(data json.RawMessage, target any) error {
	// 空 data 与显式 null 都不满足当前命令的数据要求。
	if len(data) == 0 || string(data) == "null" {
		return fmt.Errorf("%w: data is required", ErrInvalidMessage)
	}
	// 子结构解析错误统一包装为 invalid message，方便上层映射错误码。
	if err := decodeStrict(data, target); err != nil {
		return fmt.Errorf("%w: invalid data: %v", ErrInvalidMessage, err)
	}
	return nil
}

// decodeStrict 拒绝未知字段和尾随 JSON 值，保证协议演进可检测。
func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	// 第一阶段必须完整解码目标对象。
	if err := decoder.Decode(target); err != nil {
		return err
	}
	// NDJSON 要求每行恰好包含一个 JSON 值。拒绝尾随值可以避免帧边界歧义，
	// 也能防止意外夹带额外命令。
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		// 第二次解码成功意味着同一行包含多个 JSON 值，违反 NDJSON 帧约定。
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}
