package event

import (
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestValidateLifecycle(t *testing.T) {
	events := []domain.RunEvent{
		{Sequence: 1, SchemaVersion: 1, Type: domain.EventRunStarted},
		{Sequence: 2, SchemaVersion: 1, Type: domain.EventTurnStarted, TurnID: "turn-1"},
		{Sequence: 3, SchemaVersion: 1, Type: domain.EventModelStarted, TurnID: "turn-1"},
		{Sequence: 4, SchemaVersion: 1, Type: domain.EventToolStarted, TurnID: "turn-1", Payload: map[string]any{"tool_call_id": "call-1"}},
		{Sequence: 5, SchemaVersion: 1, Type: domain.EventToolCompleted, TurnID: "turn-1", Payload: map[string]any{"tool_call_id": "call-1"}},
		{Sequence: 6, SchemaVersion: 1, Type: domain.EventModelCompleted, TurnID: "turn-1"},
		{Sequence: 7, SchemaVersion: 1, Type: domain.EventTurnCompleted, TurnID: "turn-1"},
		{Sequence: 8, SchemaVersion: 1, Type: domain.EventRunCompleted},
	}
	if err := ValidateLifecycle(events); err != nil {
		t.Fatal(err)
	}
}

func TestValidateLifecycleRejectsMissingTerminalEvent(t *testing.T) {
	events := []domain.RunEvent{{Sequence: 1, SchemaVersion: 1, Type: domain.EventTurnStarted, TurnID: "turn-1"}}
	if err := ValidateLifecycle(events); err == nil {
		t.Fatal("expected lifecycle validation error")
	}
}

func TestValidateLifecycleRejectsSequenceGap(t *testing.T) {
	events := []domain.RunEvent{{Sequence: 2, SchemaVersion: 1, Type: domain.EventRunStarted}}
	if err := ValidateLifecycle(events); err == nil {
		t.Fatal("expected sequence validation error")
	}
}
