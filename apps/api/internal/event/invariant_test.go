package event

import (
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestRuntimeInvariantFailuresUseStableEventCodes(t *testing.T) {
	tests := []struct {
		name   string
		events []domain.RunEvent
		code   string
	}{
		{name: "sequence gap", events: []domain.RunEvent{{ID: "event-2", RunID: "run-1", Sequence: 2, SchemaVersion: 1, Type: domain.EventRunStarted}}, code: "event_sequence_gap"},
		{name: "orphan tool terminal", events: []domain.RunEvent{{ID: "event-1", RunID: "run-1", Sequence: 1, SchemaVersion: 1, Type: domain.EventToolFailed, Payload: map[string]any{"tool_call_id": "call-1"}}}, code: "tool_terminal_orphan"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failures := CheckRuntimeInvariants(domain.Run{ID: "run-1", Status: domain.RunCompleted}, test.events)
			if len(failures) != 1 || failures[0].Code != test.code || failures[0].Owner != "event" || failures[0].RunID != "run-1" {
				t.Fatalf("unexpected failures: %#v", failures)
			}
		})
	}
}

func TestRuntimeInvariantAllowsOpenScopesWhileRunIsActive(t *testing.T) {
	events := []domain.RunEvent{{ID: "event-1", RunID: "run-1", Sequence: 1, SchemaVersion: 1, Type: domain.EventTurnStarted, TurnID: "turn-1"}}
	if failures := CheckRuntimeInvariants(domain.Run{ID: "run-1", Status: domain.RunRunning}, events); len(failures) != 0 {
		t.Fatalf("active run should allow open scopes: %#v", failures)
	}
}
