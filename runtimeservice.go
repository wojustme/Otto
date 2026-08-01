package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/wojustme/otto/internal/desktop"
	"github.com/wojustme/otto/protocol"
)

// RuntimeInfo 是前端查询 Runtime 连接状态时收到的稳定数据结构。
type RuntimeInfo struct {
	Connected       bool   `json:"connected"`
	RuntimeVersion  string `json:"runtimeVersion"`
	ProtocolVersion int    `json:"protocolVersion"`
}

// RuntimeService 是 Wails 暴露给 React 的后端门面，负责 sidecar 生命周期和命令发送。
type RuntimeService struct {
	client *desktop.RuntimeClient
	// counter 用于区分同一毫秒内创建的 ID。这些 ID 只用于关联协议消息，
	// 不能作为身份认证令牌。
	counter atomic.Uint64
}

// NewRuntimeService 创建尚未启动 sidecar 的 Wails 服务实例。
func NewRuntimeService() *RuntimeService {
	return &RuntimeService{}
}

// ServiceStartup 在 Wails 服务启动阶段创建 agentd，并把协议事件转发给前端。
func (s *RuntimeService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	// 使用 Wails 服务上下文启动 agentd，使应用关闭时也会取消受监管的子进程。
	client := desktop.NewRuntimeClient()
	// sidecar 启动或 ready 握手失败时阻止服务进入半连接状态。
	if err := client.Start(ctx, func(event protocol.Event) {
		// 原样转发协议事件，使 React 与未来移动端或 Relay 客户端可以消费同一种事件模型。
		// 应用实例只在 Wails 生命周期有效时存在；关闭阶段不再向前端发事件。
		if app := application.Get(); app != nil {
			app.Event.Emit(runtimeEventName, event)
		}
	}); err != nil {
		return fmt.Errorf("start agent runtime: %w", err)
	}
	s.client = client
	return nil
}

// ServiceShutdown 在 Wails 退出阶段停止并回收 agentd 子进程。
func (s *RuntimeService) ServiceShutdown() error {
	// 启动未完成或已经停止时无需重复清理。
	if s.client == nil {
		return nil
	}
	return s.client.Stop()
}

// RuntimeInfo 返回当前连接状态、Runtime 版本和协议版本，供界面初始化使用。
func (s *RuntimeService) RuntimeInfo() RuntimeInfo {
	// sidecar 尚未创建时仍返回协议版本，使界面可以展示固定兼容性信息。
	if s.client == nil {
		return RuntimeInfo{ProtocolVersion: protocol.Version}
	}
	status := s.client.Status()
	return RuntimeInfo{
		Connected: status.Connected, RuntimeVersion: status.RuntimeVersion,
		ProtocolVersion: protocol.Version,
	}
}

// StartRun 校验用户消息、创建关联 ID，并向 agentd 发送任务启动命令。
func (s *RuntimeService) StartRun(message string) (string, error) {
	// client 为空说明 Wails 服务尚未完成启动或已经关闭。
	if s.client == nil {
		return "", errors.New("runtime is not connected")
	}
	message = strings.TrimSpace(message)
	// 去除空白后没有内容的消息不应创建任务。
	if message == "" {
		return "", errors.New("message is required")
	}
	// runID 贯穿完整 Agent 任务；requestID 只标识当前命令及其即时响应或事件批次。
	runID := s.nextID("run")
	requestID := s.nextID("request")
	// 命令发送失败时不向前端返回 runID，避免界面追踪一个从未启动的任务。
	if err := s.client.Send(protocol.NewRunStart(requestID, runID, message)); err != nil {
		return "", err
	}
	return runID, nil
}

// RespondToApproval 把用户的允许或拒绝决定发送给指定任务。
func (s *RuntimeService) RespondToApproval(runID, approvalID string, approved bool) error {
	// 未连接 Runtime 时无法恢复等待审批的任务。
	if s.client == nil {
		return errors.New("runtime is not connected")
	}
	// runID 用于定位任务，approvalID 用于绑定具体工具调用，两者都不可缺少。
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(approvalID) == "" {
		return errors.New("run id and approval id are required")
	}
	return s.client.Send(protocol.NewApprovalResponse(
		s.nextID("request"), runID, approvalID, approved,
	))
}

// CancelRun 请求 agentd 取消指定的非终态任务。
func (s *RuntimeService) CancelRun(runID string) error {
	// 未连接 Runtime 时没有可取消的远端状态。
	if s.client == nil {
		return errors.New("runtime is not connected")
	}
	// 空 runID 无法定位目标任务。
	if strings.TrimSpace(runID) == "" {
		return errors.New("run id is required")
	}
	return s.client.Send(protocol.NewRunCancel(s.nextID("request"), runID))
}

// nextID 使用时间戳和原子计数生成当前桌面进程内不重复的关联 ID。
func (s *RuntimeService) nextID(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixMilli(), s.counter.Add(1))
}
