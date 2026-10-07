package projection

import (
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestPartialOutputRecoveryReplacesInsteadOfAppending(t *testing.T) {
	events := []domain.RunEvent{}
	add := func(seq int64, text, status string) {
		events = append(events, domain.RunEvent{Type: domain.EventModelOutputCheckpoint, RunID: "run", TurnID: "turn", Sequence: seq,
			Payload: map[string]any{"channel": "answer", "revision": seq, "offset": len(text), "text": text, "status": status}})
	}
	add(1, "old draft", "provisional")
	add(2, "", "retracted")
	add(3, "new", "provisional")
	add(3, "new", "provisional")
	run := domain.Run{ID: "run", Status: domain.RunFailedRecoverable}
	items := BuildSnapshot(run, events, domain.RunUsageLedger{}, nil).PartialOutputs
	if len(items) != 1 || items[0].Text != "new" || items[0].Status != "interrupted" {
		t.Fatalf("recovery=%#v", items)
	}
	add(4, "", "retracted")
	if items := BuildSnapshot(run, events, domain.RunUsageLedger{}, nil).PartialOutputs; len(items) != 0 {
		t.Fatalf("reset resurrected: %#v", items)
	}
	add(5, "done", "final")
	run.Status = domain.RunCompleted
	if items := BuildSnapshot(run, events, domain.RunUsageLedger{}, nil).PartialOutputs; len(items) != 0 {
		t.Fatalf("second final answer: %#v", items)
	}
}

func TestPartialOutputBeforeResumeRemainsIncomplete(t *testing.T) {
	events := []domain.RunEvent{
		{Type: domain.EventModelOutputCheckpoint, RunID: "r", TurnID: "old", Sequence: 1,
			Payload: map[string]any{"channel": "answer", "round": 1, "revision": 1, "offset": 3, "text": "old", "status": "provisional"}},
		{Type: domain.EventRunResumed, RunID: "r", Sequence: 2},
	}
	items := BuildSnapshot(domain.Run{ID: "r", Status: domain.RunRunning}, events, domain.RunUsageLedger{}, nil).PartialOutputs
	if len(items) != 1 || items[0].Status != "interrupted" {
		t.Fatalf("old attempt looks active: %#v", items)
	}
}
