package protocol

import (
	"errors"
	"testing"
)

func TestDecodeRunStart(t *testing.T) {
	command, err := DecodeCommand([]byte(`{"protocolVersion":1,"type":"run.start","requestId":"request-1","runId":"run-1","data":{"message":"hello"}}`))
	if err != nil {
		t.Fatalf("DecodeCommand() error = %v", err)
	}
	start, ok := command.(RunStart)
	if !ok {
		t.Fatalf("command type = %T", command)
	}
	if start.Message != "hello" || start.RunID != "run-1" {
		t.Fatalf("decoded command = %#v", start)
	}
}

func TestDecodeCommandRejectsUnknownFields(t *testing.T) {
	_, err := DecodeCommand([]byte(`{"protocolVersion":1,"type":"run.cancel","requestId":"request-1","runId":"run-1","extra":true}`))
	if !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("error = %v, want ErrInvalidMessage", err)
	}
}

func TestDecodeCommandRejectsUnsupportedVersion(t *testing.T) {
	_, err := DecodeCommand([]byte(`{"protocolVersion":2,"type":"run.cancel","requestId":"request-1","runId":"run-1"}`))
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("error = %v, want ErrUnsupportedVersion", err)
	}
}
