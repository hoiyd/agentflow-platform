package event

import (
	"fmt"
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

func TestRuntimeInvariantReportsRunLifecycleFailures(t *testing.T) {
	tests := []struct {
		name   string
		status domain.RunStatus
		events []domain.RunEvent
		code   string
	}{
		{
			name: "transition without active lifecycle", status: domain.RunRunning,
			events: []domain.RunEvent{runtimeEvent(1, domain.EventRunWaitingForUser)}, code: "run_transition_orphan",
		},
		{
			name: "terminal without start", status: domain.RunCompleted,
			events: []domain.RunEvent{runtimeEvent(1, domain.EventRunCompleted)}, code: "run_terminal_orphan",
		},
		{
			name: "duplicate terminal", status: domain.RunCompleted,
			events: []domain.RunEvent{
				runtimeEvent(1, domain.EventRunCreated), runtimeEvent(2, domain.EventRunCompleted), runtimeEvent(3, domain.EventRunFailed),
			}, code: "run_terminal_duplicate",
		},
		{
			name: "restart after terminal", status: domain.RunCompleted,
			events: []domain.RunEvent{
				runtimeEvent(1, domain.EventRunCreated), runtimeEvent(2, domain.EventRunCompleted), runtimeEvent(3, domain.EventRunStarted),
			}, code: "run_event_after_terminal",
		},
		{
			name: "finished status without terminal", status: domain.RunFailed,
			events: []domain.RunEvent{runtimeEvent(1, domain.EventRunCreated)}, code: "run_terminal_missing",
		},
		{
			name: "terminal event with active status", status: domain.RunRunning,
			events: []domain.RunEvent{runtimeEvent(1, domain.EventRunCreated), runtimeEvent(2, domain.EventRunCompleted)}, code: "run_status_terminal_mismatch",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failures := CheckRuntimeInvariants(domain.Run{ID: "run-1", Status: test.status}, test.events)
			if len(failures) != 1 || failures[0].Code != test.code || failures[0].RunID != "run-1" {
				t.Fatalf("failures=%#v want code %q", failures, test.code)
			}
		})
	}
}

func TestRuntimeInvariantReportsOpenScopesForFinishedRun(t *testing.T) {
	tests := []struct {
		name  string
		event domain.RunEvent
		code  string
	}{
		{name: "stage", event: runtimeEvent(1, domain.EventStageStarted), code: "stage_terminal_missing"},
		{name: "turn", event: runtimeEvent(1, domain.EventTurnStarted), code: "turn_terminal_missing"},
		{name: "model", event: runtimeEvent(1, domain.EventModelStarted), code: "model_terminal_missing"},
		{name: "tool", event: runtimeEvent(1, domain.EventToolStarted), code: "tool_terminal_missing"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failures := CheckRuntimeInvariants(domain.Run{ID: "run-1", Status: domain.RunCompleted}, []domain.RunEvent{test.event})
			if len(failures) != 1 || failures[0].Code != test.code || failures[0].Sequence != 1 || failures[0].EventID != "event-1" {
				t.Fatalf("failures=%#v want code %q", failures, test.code)
			}
		})
	}
}

func TestRuntimeInvariantValidatesRegisteredEventProtocol(t *testing.T) {
	tests := []struct {
		name  string
		event domain.RunEvent
		code  string
	}{
		{name: "unknown type", event: runtimeEvent(1, domain.RunEventType("unknown.event")), code: "event_type_unregistered"},
		{name: "unsupported schema", event: runtimeEvent(1, domain.EventRunCreated), code: "event_schema_unsupported"},
	}
	tests[1].event.SchemaVersion++

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failures := CheckRuntimeInvariants(domain.Run{ID: "run-1", Status: domain.RunRunning}, []domain.RunEvent{test.event})
			if len(failures) != 1 || failures[0].Code != test.code {
				t.Fatalf("failures=%#v want code %q", failures, test.code)
			}
		})
	}
}

func TestRuntimeInvariantAllowsOpenScopesForEveryActiveStatus(t *testing.T) {
	for _, status := range []domain.RunStatus{domain.RunRunning, domain.RunQueued, domain.RunWaitingForUser, domain.RunCanceling} {
		t.Run(string(status), func(t *testing.T) {
			if failures := CheckRuntimeInvariants(domain.Run{ID: "run-1", Status: status}, []domain.RunEvent{runtimeEvent(1, domain.EventToolStarted)}); len(failures) != 0 {
				t.Fatalf("status %s should allow an open scope: %#v", status, failures)
			}
		})
	}
}

func TestRuntimeInvariantAcceptsCompletedLifecycle(t *testing.T) {
	events := []domain.RunEvent{runtimeEvent(1, domain.EventRunCreated), runtimeEvent(2, domain.EventRunCompleted)}
	if failures := CheckRuntimeInvariants(domain.Run{ID: "run-1", Status: domain.RunCompleted}, events); len(failures) != 0 {
		t.Fatalf("completed lifecycle should be valid: %#v", failures)
	}
}

func runtimeEvent(sequence int64, eventType domain.RunEventType) domain.RunEvent {
	item := domain.RunEvent{
		ID: fmt.Sprintf("event-%d", sequence), RunID: "run-1", Sequence: sequence,
		SchemaVersion: domain.CurrentRunEventSchemaVersion, Type: eventType,
	}
	switch eventType {
	case domain.EventStageStarted:
		item.StageID = "stage-1"
	case domain.EventTurnStarted, domain.EventModelStarted:
		item.TurnID = "turn-1"
	case domain.EventToolStarted:
		item.Payload = map[string]any{"tool_call_id": "tool-call-1"}
	}
	return item
}
