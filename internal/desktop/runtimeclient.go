// Package desktop 提供桌面壳适配器和 Runtime 子进程监管能力。
package desktop

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/wojustme/otto/protocol"
)

// RuntimeStatus 是提供给 Wails 服务和界面读取的 sidecar 连接快照。
type RuntimeStatus struct {
	Connected      bool
	RuntimeVersion string
}

// RuntimeClient 负责启动、监管并通过 NDJSON 与 otto-agentd 通信。
type RuntimeClient struct {
	// mu 同时保护进程句柄并串行化 NDJSON 写入。
	// statusMu 单独存在，使界面的频繁状态读取无需等待命令写入。
	mu       sync.Mutex
	statusMu sync.RWMutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	encoder  *json.Encoder
	done     chan error
	status   RuntimeStatus
}

// NewRuntimeClient 创建尚未启动的 Runtime 客户端。
func NewRuntimeClient() *RuntimeClient {
	return &RuntimeClient{}
}

// Start 解析 sidecar 路径、启动进程、消费输出，并等待 runtime.ready 握手。
func (c *RuntimeClient) Start(ctx context.Context, onEvent func(protocol.Event)) error {
	// 没有回调会导致事件无人消费，也无法把状态转发给界面。
	if onEvent == nil {
		return errors.New("event callback is required")
	}
	path, err := resolveRuntimePath()
	// 找不到可执行文件时不尝试启动空命令。
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, path)
	stdin, err := cmd.StdinPipe()
	// stdin 是向 sidecar 写入命令的唯一通道。
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	// stdout 承载结构化事件，创建失败时无法建立协议连接。
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	// stderr 单独承载日志，避免与 stdout 的 NDJSON 混流。
	if err != nil {
		return err
	}
	// OS 拒绝启动或文件不可执行时，返回带路径的上下文错误。
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", path, err)
	}

	c.mu.Lock()
	c.cmd = cmd
	c.stdin = stdin
	c.encoder = json.NewEncoder(stdin)
	c.done = make(chan error, 1)
	done := c.done
	c.mu.Unlock()

	// 进程创建成功并不代表 Runtime 已经可用；收到明确的 runtime.ready 握手后，
	// 才向 Wails 暴露已连接状态。
	ready := make(chan struct{})
	var readyOnce sync.Once
	go c.readEvents(stdout, onEvent, ready, &readyOnce)
	go c.readLogs(stderr)
	go func() {
		err := cmd.Wait()
		c.setStatus(RuntimeStatus{})
		done <- err
	}()

	select {
	case <-ready:
		// 收到 ready 后连接才算建立成功。
		return nil
	case err := <-done:
		// ready 前进程已经退出，说明 Runtime 初始化失败。
		if err == nil {
			// 即使退出码为零，只要没有 ready 也不能视为可用 Runtime。
			return errors.New("agent runtime exited before ready")
		}
		return fmt.Errorf("agent runtime exited before ready: %w", err)
	case <-time.After(5 * time.Second):
		// 防止损坏或卡死的 sidecar 让桌面应用无限等待启动。
		_ = cmd.Process.Kill()
		return errors.New("agent runtime did not become ready within 5 seconds")
	case <-ctx.Done():
		// 宿主生命周期提前结束时把取消原因返回给 Wails。
		return ctx.Err()
	}
}

// Send 把一条协议命令串行编码到正在运行的 sidecar stdin。
func (c *RuntimeClient) Send(command protocol.CommandEnvelope) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	// 编码器未创建或握手未完成时不允许写入，避免命令丢失。
	if c.encoder == nil || !c.Status().Connected {
		return errors.New("agent runtime is not connected")
	}
	// 编码或管道写入失败时附加命令发送上下文。
	if err := c.encoder.Encode(command); err != nil {
		return fmt.Errorf("send runtime command: %w", err)
	}
	return nil
}

// Stop 先关闭输入请求 sidecar 正常退出，超时后再强制终止。
func (c *RuntimeClient) Stop() error {
	c.mu.Lock()
	stdin, cmd, done := c.stdin, c.cmd, c.done
	c.stdin, c.encoder, c.cmd = nil, nil, nil
	c.mu.Unlock()
	// 尚未启动或已经停止时，Stop 是幂等操作。
	if cmd == nil {
		return nil
	}
	// 关闭 stdin 会让 agentd 的 Scanner 自然结束；仅当 sidecar 在两秒内未退出时，
	// 才使用 Kill 作为有界兜底。
	// 只有已建立 stdin 管道时才需要关闭。
	if stdin != nil {
		_ = stdin.Close()
	}
	select {
	case err := <-done:
		// 等待 goroutine 回收进程，避免遗留僵尸子进程。
		if err != nil {
			var exitErr *exec.ExitError
			// 普通非零退出码表示进程已成功回收；非 ExitError 才是等待本身失败。
			if !errors.As(err, &exitErr) {
				return err
			}
		}
		return nil
	case <-time.After(2 * time.Second):
		// 正常关闭超时后强制终止，保证应用退出有界。
		return cmd.Process.Kill()
	}
}

// Status 返回并发安全的当前连接状态快照。
func (c *RuntimeClient) Status() RuntimeStatus {
	c.statusMu.RLock()
	defer c.statusMu.RUnlock()
	return c.status
}

// readEvents 持续解码 sidecar stdout，并把协议事件交给上层回调。
func (c *RuntimeClient) readEvents(reader io.Reader, onEvent func(protocol.Event), ready chan struct{}, readyOnce *sync.Once) {
	// stdout 只承载事件流；stderr 单独消费，确保可读日志不会污染 NDJSON 帧。
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var event protocol.Event
		// 单行事件损坏时记录错误并继续读取，避免一条坏消息终止整个客户端。
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			slog.Error("decode runtime event", "error", err)
			continue
		}
		// ready 事件同时完成版本提取和连接状态切换。
		if event.Type == protocol.TypeRuntimeReady {
			version := "unknown"
			// Event.Data 由 encoding/json 解码为 map，类型不符时保留 unknown 版本。
			if data, ok := event.Data.(map[string]any); ok {
				// runtimeVersion 字段必须是字符串才能用于界面展示。
				if value, ok := data["runtimeVersion"].(string); ok {
					version = value
				}
			}
			c.setStatus(RuntimeStatus{Connected: true, RuntimeVersion: version})
			readyOnce.Do(func() { close(ready) })
		}
		onEvent(event)
	}
}

// readLogs 持续消费 sidecar stderr，并转入桌面应用的结构化日志。
func (c *RuntimeClient) readLogs(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		slog.Info("agentd", "message", scanner.Text())
	}
}

// setStatus 在写锁保护下替换完整状态快照。
func (c *RuntimeClient) setStatus(status RuntimeStatus) {
	c.statusMu.Lock()
	c.status = status
	c.statusMu.Unlock()
}

// resolveRuntimePath 按覆盖路径、应用包目录和开发目录的优先级查找 sidecar。
func resolveRuntimePath() (string, error) {
	name := "otto-agentd"
	// Windows 可执行文件需要 .exe 后缀，其他平台保持无后缀名称。
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	// 查找顺序依次支持显式路径覆盖、打包应用（sidecar 与主程序同目录）和仓库开发，
	// 无需在上层编写平台分支。
	var candidates []string
	// 显式环境变量优先，便于开发、测试或自定义部署替换 sidecar。
	if configured := os.Getenv("OTTO_AGENTD_PATH"); configured != "" {
		candidates = append(candidates, configured)
	}
	// 打包应用把 agentd 放在主程序同目录，因此第二优先级从可执行文件旁查找。
	if executable, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(executable), name))
	}
	// 仓库开发时 sidecar 位于工作目录下的 bin 目录。
	if workingDirectory, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(workingDirectory, "bin", name))
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		// 候选路径必须存在且是文件，目录不能作为可执行 sidecar。
		if err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("otto-agentd not found; checked %v", candidates)
}
