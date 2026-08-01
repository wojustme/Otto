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

type RuntimeInfo struct {
	Connected       bool   `json:"connected"`
	RuntimeVersion  string `json:"runtimeVersion"`
	ProtocolVersion int    `json:"protocolVersion"`
}

type RuntimeService struct {
	client  *desktop.RuntimeClient
	counter atomic.Uint64
}

func NewRuntimeService() *RuntimeService {
	return &RuntimeService{}
}

func (s *RuntimeService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	client := desktop.NewRuntimeClient()
	if err := client.Start(ctx, func(event protocol.Event) {
		if app := application.Get(); app != nil {
			app.Event.Emit(runtimeEventName, event)
		}
	}); err != nil {
		return fmt.Errorf("start agent runtime: %w", err)
	}
	s.client = client
	return nil
}

func (s *RuntimeService) ServiceShutdown() error {
	if s.client == nil {
		return nil
	}
	return s.client.Stop()
}

func (s *RuntimeService) RuntimeInfo() RuntimeInfo {
	if s.client == nil {
		return RuntimeInfo{ProtocolVersion: protocol.Version}
	}
	status := s.client.Status()
	return RuntimeInfo{
		Connected: status.Connected, RuntimeVersion: status.RuntimeVersion,
		ProtocolVersion: protocol.Version,
	}
}

func (s *RuntimeService) StartRun(message string) (string, error) {
	if s.client == nil {
		return "", errors.New("runtime is not connected")
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return "", errors.New("message is required")
	}
	runID := s.nextID("run")
	requestID := s.nextID("request")
	if err := s.client.Send(protocol.NewRunStart(requestID, runID, message)); err != nil {
		return "", err
	}
	return runID, nil
}

func (s *RuntimeService) RespondToApproval(runID, approvalID string, approved bool) error {
	if s.client == nil {
		return errors.New("runtime is not connected")
	}
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(approvalID) == "" {
		return errors.New("run id and approval id are required")
	}
	return s.client.Send(protocol.NewApprovalResponse(
		s.nextID("request"), runID, approvalID, approved,
	))
}

func (s *RuntimeService) CancelRun(runID string) error {
	if s.client == nil {
		return errors.New("runtime is not connected")
	}
	if strings.TrimSpace(runID) == "" {
		return errors.New("run id is required")
	}
	return s.client.Send(protocol.NewRunCancel(s.nextID("request"), runID))
}

func (s *RuntimeService) nextID(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixMilli(), s.counter.Add(1))
}
