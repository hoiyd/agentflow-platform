package projection

import (
	"fmt"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

// Failure inventory: malformed progress is ignored; terminal cancellation and
// crash repair stay incomplete; Resume cannot make an old update live again;
// calls without progress remain absent and the read model stays bounded.
func TestToolProgressRecoveryAndBounds(t *testing.T) {
	events := []domain.RunEvent{}
	add := func(kind domain.RunEventType, call string, payload map[string]any) {
		if payload == nil {
			payload = map[string]any{}
		}
		payload["tool_call_id"], payload["tool_name"] = call, "reader"
		events = append(events, domain.RunEvent{Type: kind, RunID: "r", TurnID: "t", Sequence: int64(len(events) + 1), Payload: payload})
	}
	add(domain.EventToolProgress, "bad", map[string]any{"phase": "bad phase"})
	add(domain.EventToolProgress, "invalid", map[string]any{"phase": "reading", "completed": "invalid"})
	add(domain.EventToolCompleted, "silent", nil)
	add(domain.EventToolProgress, "old", map[string]any{"phase": "reading"})
	add(domain.EventRunResumed, "", nil)
	add(domain.EventToolProgress, "new", map[string]any{"phase": "reading"})
	add(domain.EventToolFailed, "new", map[string]any{"error_code": "execution_canceled"})
	items := buildToolProgress(domain.Run{Status: domain.RunRunning}, events)
	if len(items) != 2 || items[0].Status != "interrupted" || items[1].Status != "interrupted" {
		t.Fatalf("resumed/canceled view=%#v", items)
	}
	for _, code := range []string{"execution_timeout", "execution_failed", "synthetic"} {
		add(domain.EventToolProgress, code, map[string]any{"phase": "reading"})
		add(domain.EventToolFailed, code, map[string]any{"error_code": code, "synthetic": code == "synthetic"})
	}
	items = buildToolProgress(domain.Run{Status: domain.RunFailed}, events)
	if items[2].Status != "interrupted" || items[3].Status != "failed" || items[4].Status != "interrupted" {
		t.Fatalf("failure view=%#v", items)
	}
	for i := range 40 {
		add(domain.EventToolProgress, fmt.Sprint(i), map[string]any{"phase": "reading"})
	}
	items = buildToolProgress(domain.Run{Status: domain.RunRunning}, events)
	if len(items) != 32 || items[0].ToolCallID != "8" || items[31].ToolCallID != "39" {
		t.Fatalf("unbounded read model=%#v", items)
	}
}
