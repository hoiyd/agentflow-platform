package event

import (
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestValidateLifecycle(t *testing.T) {
	events := []domain.RunEvent{
		{Sequence: 1, SchemaVersion: 1, Type: domain.EventRunStarted},
		{Sequence: 2, SchemaVersion: 1, Type: domain.EventStageStarted, StageID: "stage-1"},
		{Sequence: 3, SchemaVersion: 1, Type: domain.EventTurnStarted, StageID: "stage-1", TurnID: "turn-1"},
		{Sequence: 4, SchemaVersion: 1, Type: domain.EventModelStarted, StageID: "stage-1", TurnID: "turn-1"},
		{Sequence: 5, SchemaVersion: 1, Type: domain.EventToolStarted, StageID: "stage-1", TurnID: "turn-1", Payload: map[string]any{"tool_call_id": "call-1"}},
		{Sequence: 6, SchemaVersion: 1, Type: domain.EventToolCompleted, StageID: "stage-1", TurnID: "turn-1", Payload: map[string]any{"tool_call_id": "call-1"}},
		{Sequence: 7, SchemaVersion: 1, Type: domain.EventModelCompleted, StageID: "stage-1", TurnID: "turn-1"},
		{Sequence: 8, SchemaVersion: 1, Type: domain.EventTurnCompleted, StageID: "stage-1", TurnID: "turn-1"},
		{Sequence: 9, SchemaVersion: 1, Type: domain.EventStageCompleted, StageID: "stage-1"},
		{Sequence: 10, SchemaVersion: 1, Type: domain.EventRunCompleted},
	}
	if err := ValidateLifecycle(events); err != nil {
		t.Fatal(err)
	}
}

func TestPlanInterruptedLifecycleRepairClosesInnerScopesFirst(t *testing.T) {
	events := []domain.RunEvent{
		{ID: "stage-start", Sequence: 1, SchemaVersion: 1, Type: domain.EventStageStarted, RunID: "run-1", StageID: "stage-1", Payload: map[string]any{"name": "worker"}},
		{ID: "turn-start", Sequence: 2, SchemaVersion: 1, Type: domain.EventTurnStarted, RunID: "run-1", StageID: "stage-1", TurnID: "turn-1"},
		{ID: "model-start", Sequence: 3, SchemaVersion: 1, Type: domain.EventModelStarted, RunID: "run-1", StageID: "stage-1", TurnID: "turn-1"},
		{ID: "tool-start", Sequence: 4, SchemaVersion: 1, Type: domain.EventToolStarted, RunID: "run-1", StageID: "stage-1", TurnID: "turn-1", Payload: map[string]any{"tool_call_id": "call-1"}},
	}
	terminal, err := PlanInterruptedLifecycleRepair(events, domain.InterruptedWorkerReason)
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.RunEventType{domain.EventToolFailed, domain.EventModelFailed, domain.EventTurnFailed, domain.EventStageFailed}
	if len(terminal) != len(want) {
		t.Fatalf("expected %d terminal events, got %d", len(want), len(terminal))
	}
	for index, event := range terminal {
		if event.Type != want[index] {
			t.Fatalf("terminal %d: got %s want %s", index, event.Type, want[index])
		}
		if event.Payload["synthetic"] != true || event.Payload["reason"] != domain.InterruptedWorkerReason {
			t.Fatalf("terminal %d missing recovery metadata: %#v", index, event.Payload)
		}
	}
	if err := ValidateLifecycle(append(events, terminal...)); err != nil {
		t.Fatalf("repaired lifecycle: %v", err)
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

func TestValidateLifecycleKeepsLegacyUnpairedStageTerminalsReadable(t *testing.T) {
	events := []domain.RunEvent{
		{Sequence: 1, SchemaVersion: 1, Type: domain.EventStageCompleted, StageID: "legacy-stage"},
	}
	if err := ValidateLifecycle(events); err != nil {
		t.Fatalf("legacy stage terminal should remain readable: %v", err)
	}
}

func TestValidateLifecycleRequiresStagePairingForCheckpointedStreams(t *testing.T) {
	events := []domain.RunEvent{
		{Sequence: 1, SchemaVersion: 1, Type: domain.EventCheckpointCaptured, StageID: "stage-1"},
		{Sequence: 2, SchemaVersion: 1, Type: domain.EventStageCompleted, StageID: "stage-1"},
	}
	if err := ValidateLifecycle(events); err == nil {
		t.Fatal("expected unmatched checkpointed stage terminal error")
	}
}

func TestValidateLifecycleRejectsInvalidScopeTransitions(t *testing.T) {
	tests := []struct {
		name   string
		events []domain.RunEvent
		want   string
	}{
		{name: "schema", events: []domain.RunEvent{{Sequence: 1, SchemaVersion: 99, Type: domain.EventRunStarted}}, want: "unsupported schema"},
		{name: "stage id", events: []domain.RunEvent{{Sequence: 1, SchemaVersion: 1, Type: domain.EventStageStarted}}, want: "invalid stage start"},
		{name: "overlapping stage", events: []domain.RunEvent{
			{Sequence: 1, SchemaVersion: 1, Type: domain.EventStageStarted, StageID: "stage-1"},
			{Sequence: 2, SchemaVersion: 1, Type: domain.EventStageStarted, StageID: "stage-1"},
		}, want: "overlapping stage"},
		{name: "turn id", events: []domain.RunEvent{{Sequence: 1, SchemaVersion: 1, Type: domain.EventTurnStarted}}, want: "invalid turn start"},
		{name: "overlapping turn", events: []domain.RunEvent{
			{Sequence: 1, SchemaVersion: 1, Type: domain.EventTurnStarted, TurnID: "turn-1"},
			{Sequence: 2, SchemaVersion: 1, Type: domain.EventTurnStarted, TurnID: "turn-1"},
		}, want: "overlapping turn"},
		{name: "unmatched turn", events: []domain.RunEvent{{Sequence: 1, SchemaVersion: 1, Type: domain.EventTurnFailed, TurnID: "turn-1"}}, want: "unmatched turn"},
		{name: "model turn", events: []domain.RunEvent{{Sequence: 1, SchemaVersion: 1, Type: domain.EventModelStarted}}, want: "invalid model start"},
		{name: "overlapping model", events: []domain.RunEvent{
			{Sequence: 1, SchemaVersion: 1, Type: domain.EventModelStarted, TurnID: "turn-1"},
			{Sequence: 2, SchemaVersion: 1, Type: domain.EventModelStarted, TurnID: "turn-1"},
		}, want: "overlapping model"},
		{name: "unmatched model", events: []domain.RunEvent{{Sequence: 1, SchemaVersion: 1, Type: domain.EventModelFailed, TurnID: "turn-1"}}, want: "unmatched model"},
		{name: "tool call id", events: []domain.RunEvent{{Sequence: 1, SchemaVersion: 1, Type: domain.EventToolStarted}}, want: "invalid tool start"},
		{name: "overlapping tool", events: []domain.RunEvent{
			{Sequence: 1, SchemaVersion: 1, Type: domain.EventToolStarted, Payload: map[string]any{"tool_call_id": "call-1"}},
			{Sequence: 2, SchemaVersion: 1, Type: domain.EventToolStarted, Payload: map[string]any{"tool_call_id": "call-1"}},
		}, want: "overlapping tool"},
		{name: "unmatched tool", events: []domain.RunEvent{{Sequence: 1, SchemaVersion: 1, Type: domain.EventToolFailed, Payload: map[string]any{"tool_call_id": "call-1"}}}, want: "unmatched tool"},
		{name: "open stage", events: []domain.RunEvent{{Sequence: 1, SchemaVersion: 1, Type: domain.EventStageStarted, StageID: "stage-1"}}, want: "stage stage-1"},
		{name: "open model", events: []domain.RunEvent{{Sequence: 1, SchemaVersion: 1, Type: domain.EventModelStarted, TurnID: "turn-1"}}, want: "model call"},
		{name: "open tool", events: []domain.RunEvent{{Sequence: 1, SchemaVersion: 1, Type: domain.EventToolStarted, Payload: map[string]any{"tool_call_id": "call-1"}}}, want: "tool call"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateLifecycle(test.events)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q error, got %v", test.want, err)
			}
		})
	}
}

func TestPlanInterruptedLifecycleRepairRejectsInvalidEventStream(t *testing.T) {
	_, err := PlanInterruptedLifecycleRepair([]domain.RunEvent{{
		Sequence: 2, SchemaVersion: 1, Type: domain.EventRunStarted,
	}}, domain.InterruptedWorkerReason)
	if err == nil {
		t.Fatal("expected invalid event stream error")
	}
}
