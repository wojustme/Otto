// Package agentd exposes the agent engine through NDJSON over stdin/stdout.
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

const Version = "0.1.0"

type Server struct {
	input  io.Reader
	output io.Writer
	engine *agent.Engine
	clock  agent.Clock
	logger *slog.Logger
}

func New(input io.Reader, output io.Writer, engine *agent.Engine, logger *slog.Logger) (*Server, error) {
	if input == nil || output == nil || engine == nil {
		return nil, errors.New("input, output, and engine are required")
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Server{input: input, output: output, engine: engine, clock: agent.SystemClock{}, logger: logger}, nil
}

func (s *Server) Serve(ctx context.Context) error {
	encoder := json.NewEncoder(s.output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(protocol.NewReady(Version)); err != nil {
		return err
	}
	s.logger.Info("agent runtime ready", "version", Version)

	scanner := bufio.NewScanner(s.input)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		command, err := protocol.DecodeCommand([]byte(line))
		if err != nil {
			if writeErr := encoder.Encode(protocol.NewError(protocol.PeekRequestID([]byte(line)), errorCode(err), err.Error())); writeErr != nil {
				return writeErr
			}
			continue
		}
		if err := s.handle(ctx, encoder, command); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (s *Server) handle(ctx context.Context, encoder *json.Encoder, command protocol.Command) error {
	switch value := command.(type) {
	case protocol.RuntimePing:
		return encoder.Encode(protocol.NewPong(value.RequestID, value.SentAt, s.clock.Now()))
	case protocol.RunStart:
		events, err := s.engine.Start(ctx, value.RunID, value.Message)
		return s.writeAgentResult(encoder, value.RequestID, events, err)
	case protocol.ApprovalRespond:
		events, err := s.engine.RespondToApproval(ctx, value.RunID, value.ApprovalID, value.Approved)
		return s.writeAgentResult(encoder, value.RequestID, events, err)
	case protocol.RunCancel:
		events, err := s.engine.Cancel(value.RunID)
		return s.writeAgentResult(encoder, value.RequestID, events, err)
	default:
		return fmt.Errorf("unsupported command %T", command)
	}
}

func (s *Server) writeAgentResult(encoder *json.Encoder, requestID string, events []agent.Event, err error) error {
	if err != nil {
		return encoder.Encode(protocol.NewError(requestID, errorCode(err), err.Error()))
	}
	for _, event := range events {
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

func errorCode(err error) string {
	switch {
	case errors.Is(err, protocol.ErrUnsupportedVersion):
		return "unsupported_protocol_version"
	case errors.Is(err, protocol.ErrUnsupportedCommand):
		return "unsupported_command"
	case errors.Is(err, agent.ErrRunExists):
		return "run_exists"
	case errors.Is(err, agent.ErrRunNotFound):
		return "run_not_found"
	case errors.Is(err, agent.ErrApprovalMismatch):
		return "approval_mismatch"
	case errors.Is(err, agent.ErrInvalidState):
		return "invalid_state"
	default:
		return "invalid_request"
	}
}
