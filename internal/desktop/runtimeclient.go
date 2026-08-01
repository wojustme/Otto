// Package desktop contains desktop-shell adapters and process supervision.
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

type RuntimeStatus struct {
	Connected      bool
	RuntimeVersion string
}

type RuntimeClient struct {
	mu       sync.Mutex
	statusMu sync.RWMutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	encoder  *json.Encoder
	done     chan error
	status   RuntimeStatus
}

func NewRuntimeClient() *RuntimeClient {
	return &RuntimeClient{}
}

func (c *RuntimeClient) Start(ctx context.Context, onEvent func(protocol.Event)) error {
	if onEvent == nil {
		return errors.New("event callback is required")
	}
	path, err := resolveRuntimePath()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
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
		return nil
	case err := <-done:
		if err == nil {
			return errors.New("agent runtime exited before ready")
		}
		return fmt.Errorf("agent runtime exited before ready: %w", err)
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		return errors.New("agent runtime did not become ready within 5 seconds")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *RuntimeClient) Send(command protocol.CommandEnvelope) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.encoder == nil || !c.Status().Connected {
		return errors.New("agent runtime is not connected")
	}
	if err := c.encoder.Encode(command); err != nil {
		return fmt.Errorf("send runtime command: %w", err)
	}
	return nil
}

func (c *RuntimeClient) Stop() error {
	c.mu.Lock()
	stdin, cmd, done := c.stdin, c.cmd, c.done
	c.stdin, c.encoder, c.cmd = nil, nil, nil
	c.mu.Unlock()
	if cmd == nil {
		return nil
	}
	if stdin != nil {
		_ = stdin.Close()
	}
	select {
	case err := <-done:
		if err != nil {
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) {
				return err
			}
		}
		return nil
	case <-time.After(2 * time.Second):
		return cmd.Process.Kill()
	}
}

func (c *RuntimeClient) Status() RuntimeStatus {
	c.statusMu.RLock()
	defer c.statusMu.RUnlock()
	return c.status
}

func (c *RuntimeClient) readEvents(reader io.Reader, onEvent func(protocol.Event), ready chan struct{}, readyOnce *sync.Once) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var event protocol.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			slog.Error("decode runtime event", "error", err)
			continue
		}
		if event.Type == protocol.TypeRuntimeReady {
			version := "unknown"
			if data, ok := event.Data.(map[string]any); ok {
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

func (c *RuntimeClient) readLogs(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		slog.Info("agentd", "message", scanner.Text())
	}
}

func (c *RuntimeClient) setStatus(status RuntimeStatus) {
	c.statusMu.Lock()
	c.status = status
	c.statusMu.Unlock()
}

func resolveRuntimePath() (string, error) {
	name := "otto-agentd"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	var candidates []string
	if configured := os.Getenv("OTTO_AGENTD_PATH"); configured != "" {
		candidates = append(candidates, configured)
	}
	if executable, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(executable), name))
	}
	if workingDirectory, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(workingDirectory, "bin", name))
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("otto-agentd not found; checked %v", candidates)
}
