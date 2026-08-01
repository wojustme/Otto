// Package protocol defines the versioned boundary shared by Otto processes.
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

const Version = 1

const (
	TypeRuntimeReady    = "runtime.ready"
	TypeRuntimePing     = "runtime.ping"
	TypeRuntimePong     = "runtime.pong"
	TypeRuntimeError    = "runtime.error"
	TypeRunStart        = "run.start"
	TypeRunCancel       = "run.cancel"
	TypeApprovalRespond = "approval.respond"
)

var (
	ErrInvalidMessage     = errors.New("invalid protocol message")
	ErrUnsupportedVersion = errors.New("unsupported protocol version")
	ErrUnsupportedCommand = errors.New("unsupported protocol command")
)

type CommandEnvelope struct {
	ProtocolVersion int    `json:"protocolVersion"`
	Type            string `json:"type"`
	RequestID       string `json:"requestId"`
	RunID           string `json:"runId,omitempty"`
	Data            any    `json:"data,omitempty"`
}

type Event struct {
	ProtocolVersion int    `json:"protocolVersion"`
	Type            string `json:"type"`
	RequestID       string `json:"requestId,omitempty"`
	RunID           string `json:"runId,omitempty"`
	Sequence        uint64 `json:"sequence,omitempty"`
	OccurredAt      string `json:"occurredAt,omitempty"`
	Data            any    `json:"data,omitempty"`
}

type Command interface {
	CommandType() string
	CommandRequestID() string
}

type RuntimePing struct {
	RequestID string
	SentAt    time.Time
}

func (c RuntimePing) CommandType() string      { return TypeRuntimePing }
func (c RuntimePing) CommandRequestID() string { return c.RequestID }

type RunStart struct {
	RequestID string
	RunID     string
	Message   string
}

func (c RunStart) CommandType() string      { return TypeRunStart }
func (c RunStart) CommandRequestID() string { return c.RequestID }

type ApprovalRespond struct {
	RequestID  string
	RunID      string
	ApprovalID string
	Approved   bool
}

func (c ApprovalRespond) CommandType() string      { return TypeApprovalRespond }
func (c ApprovalRespond) CommandRequestID() string { return c.RequestID }

type RunCancel struct {
	RequestID string
	RunID     string
}

func (c RunCancel) CommandType() string      { return TypeRunCancel }
func (c RunCancel) CommandRequestID() string { return c.RequestID }

type commandHeader struct {
	ProtocolVersion int             `json:"protocolVersion"`
	Type            string          `json:"type"`
	RequestID       string          `json:"requestId"`
	RunID           string          `json:"runId,omitempty"`
	Data            json.RawMessage `json:"data,omitempty"`
}

func DecodeCommand(line []byte) (Command, error) {
	var header commandHeader
	if err := decodeStrict(line, &header); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidMessage, err)
	}
	if header.ProtocolVersion != Version {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrUnsupportedVersion, header.ProtocolVersion, Version)
	}
	if strings.TrimSpace(header.RequestID) == "" {
		return nil, fmt.Errorf("%w: requestId is required", ErrInvalidMessage)
	}

	switch header.Type {
	case TypeRuntimePing:
		var data struct {
			SentAt string `json:"sentAt"`
		}
		if err := decodeData(header.Data, &data); err != nil {
			return nil, err
		}
		sentAt, err := time.Parse(time.RFC3339Nano, data.SentAt)
		if err != nil {
			return nil, fmt.Errorf("%w: data.sentAt must be RFC3339", ErrInvalidMessage)
		}
		return RuntimePing{RequestID: header.RequestID, SentAt: sentAt}, nil

	case TypeRunStart:
		if strings.TrimSpace(header.RunID) == "" {
			return nil, fmt.Errorf("%w: runId is required", ErrInvalidMessage)
		}
		var data struct {
			Message string `json:"message"`
		}
		if err := decodeData(header.Data, &data); err != nil {
			return nil, err
		}
		if strings.TrimSpace(data.Message) == "" {
			return nil, fmt.Errorf("%w: data.message is required", ErrInvalidMessage)
		}
		return RunStart{RequestID: header.RequestID, RunID: header.RunID, Message: data.Message}, nil

	case TypeApprovalRespond:
		if strings.TrimSpace(header.RunID) == "" {
			return nil, fmt.Errorf("%w: runId is required", ErrInvalidMessage)
		}
		var data struct {
			ApprovalID string `json:"approvalId"`
			Approved   *bool  `json:"approved"`
		}
		if err := decodeData(header.Data, &data); err != nil {
			return nil, err
		}
		if data.ApprovalID == "" || data.Approved == nil {
			return nil, fmt.Errorf("%w: approvalId and approved are required", ErrInvalidMessage)
		}
		return ApprovalRespond{
			RequestID: header.RequestID, RunID: header.RunID,
			ApprovalID: data.ApprovalID, Approved: *data.Approved,
		}, nil

	case TypeRunCancel:
		if strings.TrimSpace(header.RunID) == "" {
			return nil, fmt.Errorf("%w: runId is required", ErrInvalidMessage)
		}
		return RunCancel{RequestID: header.RequestID, RunID: header.RunID}, nil

	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedCommand, header.Type)
	}
}

func NewRunStart(requestID, runID, message string) CommandEnvelope {
	return CommandEnvelope{ProtocolVersion: Version, Type: TypeRunStart, RequestID: requestID, RunID: runID,
		Data: map[string]any{"message": message}}
}

func NewApprovalResponse(requestID, runID, approvalID string, approved bool) CommandEnvelope {
	return CommandEnvelope{ProtocolVersion: Version, Type: TypeApprovalRespond, RequestID: requestID, RunID: runID,
		Data: map[string]any{"approvalId": approvalID, "approved": approved}}
}

func NewRunCancel(requestID, runID string) CommandEnvelope {
	return CommandEnvelope{ProtocolVersion: Version, Type: TypeRunCancel, RequestID: requestID, RunID: runID}
}

func NewReady(runtimeVersion string) Event {
	return Event{ProtocolVersion: Version, Type: TypeRuntimeReady,
		Data: map[string]any{"runtimeVersion": runtimeVersion}}
}

func NewPong(requestID string, sentAt, receivedAt time.Time) Event {
	return Event{ProtocolVersion: Version, Type: TypeRuntimePong, RequestID: requestID,
		Data: map[string]any{
			"sentAt":     sentAt.UTC().Format(time.RFC3339Nano),
			"receivedAt": receivedAt.UTC().Format(time.RFC3339Nano),
		}}
}

func NewError(requestID, code, message string) Event {
	return Event{ProtocolVersion: Version, Type: TypeRuntimeError, RequestID: requestID,
		Data: map[string]any{"code": code, "message": message}}
}

func PeekRequestID(line []byte) string {
	var value struct {
		RequestID string `json:"requestId"`
	}
	_ = json.Unmarshal(line, &value)
	return value.RequestID
}

func decodeData(data json.RawMessage, target any) error {
	if len(data) == 0 || string(data) == "null" {
		return fmt.Errorf("%w: data is required", ErrInvalidMessage)
	}
	if err := decodeStrict(data, target); err != nil {
		return fmt.Errorf("%w: invalid data: %v", ErrInvalidMessage, err)
	}
	return nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}
