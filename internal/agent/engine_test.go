package agent_test

import (
	"context"
	"testing"
	"time"

	"github.com/wojustme/otto/internal/agent"
	"github.com/wojustme/otto/internal/fake"
)

type fixedClock struct{ value time.Time }

func (clock fixedClock) Now() time.Time { return clock.value }

type fixedIDs struct{ value string }

func (ids fixedIDs) NewID(string) (string, error) { return ids.value, nil }

func TestEngineApprovalFlow(t *testing.T) {
	engine := newEngine(t)
	events, err := engine.Start(context.Background(), "run-1", "整理下载目录")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	assertEventTypes(t, events,
		agent.EventRunStarted,
		agent.EventModelTextDelta,
		agent.EventToolRequested,
		agent.EventApprovalRequested,
	)
	approval := events[3].Data.(agent.ApprovalRequestedData)
	if approval.ApprovalID != "approval-fixed" {
		t.Fatalf("approval id = %q", approval.ApprovalID)
	}

	events, err = engine.RespondToApproval(context.Background(), "run-1", approval.ApprovalID, true)
	if err != nil {
		t.Fatalf("RespondToApproval() error = %v", err)
	}
	assertEventTypes(t, events,
		agent.EventApprovalResolved,
		agent.EventToolStarted,
		agent.EventToolCompleted,
		agent.EventModelTextDelta,
		agent.EventModelResponseCompleted,
		agent.EventRunCompleted,
	)
	if events[len(events)-1].Sequence != 10 {
		t.Fatalf("final sequence = %d, want 10", events[len(events)-1].Sequence)
	}
	completed := events[len(events)-1].Data.(agent.RunCompletedData)
	if completed.Output == "" {
		t.Fatal("completed output is empty")
	}
}

func TestEngineDeniedApprovalStillFinishes(t *testing.T) {
	engine := newEngine(t)
	events, err := engine.Start(context.Background(), "run-2", "删除缓存")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	approval := events[3].Data.(agent.ApprovalRequestedData)
	events, err = engine.RespondToApproval(context.Background(), "run-2", approval.ApprovalID, false)
	if err != nil {
		t.Fatalf("RespondToApproval() error = %v", err)
	}
	assertEventTypes(t, events,
		agent.EventApprovalResolved,
		agent.EventToolDenied,
		agent.EventModelTextDelta,
		agent.EventModelResponseCompleted,
		agent.EventRunCompleted,
	)
}

func newEngine(t *testing.T) *agent.Engine {
	t.Helper()
	engine, err := agent.NewEngine(agent.EngineOptions{
		Model:  fake.Model{},
		Tools:  []agent.Tool{fake.EchoTool{}},
		Policy: fake.RequireApprovalPolicy{},
		Clock:  fixedClock{value: time.Date(2026, time.August, 1, 10, 0, 0, 0, time.UTC)},
		IDs:    fixedIDs{value: "approval-fixed"},
	})
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	return engine
}

func assertEventTypes(t *testing.T, events []agent.Event, expected ...agent.EventType) {
	t.Helper()
	if len(events) != len(expected) {
		t.Fatalf("event count = %d, want %d", len(events), len(expected))
	}
	for index := range expected {
		if events[index].Type != expected[index] {
			t.Fatalf("event[%d] = %q, want %q", index, events[index].Type, expected[index])
		}
	}
}
