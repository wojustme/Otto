package agentd

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/wojustme/otto/internal/agent"
	"github.com/wojustme/otto/internal/fake"
	"github.com/wojustme/otto/protocol"
)

type fixedIDs struct{}

func (fixedIDs) NewID(string) (string, error) { return "approval-1", nil }

func TestServerStreamsVersionedEvents(t *testing.T) {
	engine, err := agent.NewEngine(agent.EngineOptions{
		Model: fake.Model{}, Tools: []agent.Tool{fake.EchoTool{}},
		Policy: fake.RequireApprovalPolicy{}, IDs: fixedIDs{},
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	input := bytes.NewBufferString(
		`{"protocolVersion":1,"type":"run.start","requestId":"request-1","runId":"run-1","data":{"message":"hello"}}` + "\n" +
			`{"protocolVersion":1,"type":"approval.respond","requestId":"request-2","runId":"run-1","data":{"approvalId":"approval-1","approved":true}}` + "\n",
	)
	var output bytes.Buffer
	server, err := New(input, &output, engine, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := server.Serve(context.Background()); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}

	decoder := json.NewDecoder(&output)
	var events []protocol.Event
	for decoder.More() {
		var event protocol.Event
		if err := decoder.Decode(&event); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		events = append(events, event)
	}
	if len(events) != 11 {
		t.Fatalf("event count = %d, want 11", len(events))
	}
	if events[0].Type != protocol.TypeRuntimeReady || events[0].ProtocolVersion != protocol.Version {
		t.Fatalf("ready event = %#v", events[0])
	}
	if events[len(events)-1].Type != string(agent.EventRunCompleted) {
		t.Fatalf("final event type = %q", events[len(events)-1].Type)
	}
}
