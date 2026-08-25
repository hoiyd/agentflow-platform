package event

import (
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestNewRunEventAcceptsCompatibleTypedPayload(t *testing.T) {
	event, err := NewRunEvent(domain.EventStageStarted, EventMetadata{RunID: "run-1", StageID: "stage-1"}, StagePayload{
		Name: "plan", AgentID: "agent-1", Iteration: 2,
	})
	if err != nil {
		t.Fatalf("new run event: %v", err)
	}
	if event.SchemaVersion != domain.CurrentRunEventSchemaVersion || event.Payload["name"] != "plan" {
		t.Fatalf("unexpected event: %#v", event)
	}
}

func TestNewRunEventRejectsMismatchedPayloadFamily(t *testing.T) {
	_, err := NewRunEvent(domain.EventToolStarted, EventMetadata{RunID: "run-1"}, StagePayload{Name: "plan"})
	if err == nil {
		t.Fatal("expected mismatched payload contract error")
	}
}

func TestTracePayloadIsBoundToOneEventType(t *testing.T) {
	payload := TracePayload{EventType: domain.EventModelStarted, Fields: map[string]any{"model": "test"}}
	if _, err := NewRunEvent(domain.EventModelCompleted, EventMetadata{RunID: "run-1"}, payload); err == nil {
		t.Fatal("expected trace payload event type mismatch")
	}
}
