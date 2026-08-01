// Package agentd 通过标准输入输出上的 NDJSON 协议暴露 Agent 引擎。
package agentd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/wojustme/otto/internal/agent"
	"github.com/wojustme/otto/protocol"
)

// Version 是 Agent Runtime 自身的实现版本，用于 ready 握手和界面展示。
// 它与 protocol.Version 的兼容性版本职责不同。
const Version = "0.1.0"

// Server 把传输无关的 Agent 引擎暴露为 stdin/stdout NDJSON 服务。
type Server struct {
	input  io.Reader
	output io.Writer
	engine *agent.Engine
	clock  agent.Clock
	logger *slog.Logger
}

// New 创建 Runtime Server，并校验协议流和 Agent 引擎依赖。
func New(input io.Reader, output io.Writer, engine *agent.Engine, logger *slog.Logger) (*Server, error) {
	// 缺少输入、输出或引擎时，Server 无法完成基本命令处理。
	if input == nil || output == nil || engine == nil {
		return nil, errors.New("input, output, and engine are required")
	}
	// logger 可选；未提供时使用静默实现，避免把日志误写入协议输出。
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Server{input: input, output: output, engine: engine, clock: agent.SystemClock{}, logger: logger}, nil
}

// Serve 发送 ready 握手，然后逐行读取、解析并处理命令，直到输入结束或发生致命错误。
func (s *Server) Serve(ctx context.Context) error {
	// stdout 专用于机器可读的 NDJSON。所有诊断信息都通过 logger 输出到 stderr，
	// 避免破坏协议帧边界。
	encoder := json.NewEncoder(s.output)
	encoder.SetEscapeHTML(false)
	// ready 写入失败意味着桌面端永远无法确认连接，应立即终止服务。
	if err := encoder.Encode(protocol.NewReady(Version)); err != nil {
		return err
	}
	s.logger.Info("agent runtime ready", "version", Version)

	scanner := bufio.NewScanner(s.input)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	// Protocol v1 串行处理命令，以保持事件批次顺序稳定且行为可预测。
	// 等取消语义明确后，可以在不改变协议的前提下引入长任务并发调度。
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// 忽略空行，使手工调试和不同平台的换行行为不会产生无效命令。
		if line == "" {
			continue
		}
		// 上下文取消表示宿主应用正在关闭或主动终止 Runtime。
		if err := ctx.Err(); err != nil {
			return err
		}
		command, err := protocol.DecodeCommand([]byte(line))
		// 协议解析错误只影响当前命令；返回 runtime.error 后继续服务下一行。
		if err != nil {
			// 如果连错误事件都无法写出，传输已经不可用，只能终止服务。
			if writeErr := encoder.Encode(protocol.NewError(protocol.PeekRequestID([]byte(line)), errorCode(err), err.Error())); writeErr != nil {
				return writeErr
			}
			continue
		}
		// 已解析命令处理失败通常意味着 stdout 写入失败等致命传输问题。
		if err := s.handle(ctx, encoder, command); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// handle 按具体命令类型调用引擎，并把产生的事件写回协议流。
func (s *Server) handle(ctx context.Context, encoder *json.Encoder, command protocol.Command) error {
	switch value := command.(type) {
	case protocol.RuntimePing:
		// Ping 不进入 Agent 引擎，只验证进程存活和协议链路。
		return encoder.Encode(protocol.NewPong(value.RequestID, value.SentAt, s.clock.Now()))
	case protocol.RunStart:
		// 启动命令创建新任务并同步推进到完成或等待审批。
		events, err := s.engine.Start(ctx, value.RunID, value.Message)
		return s.writeAgentResult(encoder, value.RequestID, events, err)
	case protocol.ApprovalRespond:
		// 审批响应恢复暂停的任务，并可能继续工具和模型回合。
		events, err := s.engine.RespondToApproval(ctx, value.RunID, value.ApprovalID, value.Approved)
		return s.writeAgentResult(encoder, value.RequestID, events, err)
	case protocol.RunCancel:
		// 取消命令只改变引擎任务状态，不直接操作 OS 进程。
		events, err := s.engine.Cancel(value.RunID)
		return s.writeAgentResult(encoder, value.RequestID, events, err)
	default:
		// DecodeCommand 正常情况下不会产生未知实现；此分支防御内部扩展遗漏。
		return fmt.Errorf("unsupported command %T", command)
	}
}

// writeAgentResult 把领域事件转换为带协议元数据的 NDJSON 事件。
func (s *Server) writeAgentResult(encoder *json.Encoder, requestID string, events []agent.Event, err error) error {
	// 引擎拒绝命令时只返回一条结构化错误，不写入可能不完整的事件集合。
	if err != nil {
		return encoder.Encode(protocol.NewError(requestID, errorCode(err), err.Error()))
	}
	// 引擎负责领域事件的数据与顺序；agentd 只添加传输元数据，
	// 使 internal/agent 不依赖具体进程通信方式。
	for _, event := range events {
		// 任意事件写入失败都说明传输断开，停止继续输出后续事件。
		if err := encoder.Encode(protocol.Event{
			ProtocolVersion: protocol.Version,
			Type:            string(event.Type),
			RequestID:       requestID,
			RunID:           event.RunID,
			Sequence:        event.Sequence,
			OccurredAt:      event.OccurredAt.Format(time.RFC3339Nano),
			Data:            event.Data,
		}); err != nil {
			return err
		}
	}
	return nil
}

// errorCode 把 Go 错误链映射为稳定的协议错误码，供不同客户端统一处理。
func errorCode(err error) string {
	switch {
	case errors.Is(err, protocol.ErrUnsupportedVersion):
		// 客户端与 Runtime 使用了不兼容的协议版本。
		return "unsupported_protocol_version"
	case errors.Is(err, protocol.ErrUnsupportedCommand):
		// 客户端发送了当前 Runtime 不认识的命令类型。
		return "unsupported_command"
	case errors.Is(err, agent.ErrRunExists):
		// runID 已被占用，不能覆盖原任务。
		return "run_exists"
	case errors.Is(err, agent.ErrRunNotFound):
		// 审批或取消命令引用了不存在的任务。
		return "run_not_found"
	case errors.Is(err, agent.ErrApprovalMismatch):
		// approvalID 与任务当前等待的审批不匹配。
		return "approval_mismatch"
	case errors.Is(err, agent.ErrInvalidState):
		// 命令与任务当前生命周期状态不兼容。
		return "invalid_state"
	default:
		// 其他校验或领域错误统一归类为无效请求。
		return "invalid_request"
	}
}
