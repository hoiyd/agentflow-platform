package projection

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func TestBuildSnapshotProducesDeterministicReadModelsAtOneWatermark(t *testing.T) {
	started := time.Date(2026, 8, 25, 8, 0, 0, 0, time.UTC)
	completed := started.Add(3 * time.Second)
	run := domain.Run{
		ID: "run-1", ConversationID: "conversation-1", Status: domain.RunCompleted,
		VerificationStatus: domain.VerificationPassed, StartedAt: &started, CompletedAt: &completed, UpdatedAt: completed,
	}
	events := []domain.RunEvent{
		{Sequence: 1, Type: domain.EventStageStarted, StageID: "stage-1"},
		{Sequence: 2, Type: domain.EventTurnStarted, TurnID: "turn-1"},
		{Sequence: 3, Type: domain.EventModelStarted, TurnID: "turn-1", Payload: map[string]any{"model_call_id": "model-1"}},
		{Sequence: 4, Type: domain.EventModelCompleted, TurnID: "turn-1", Payload: map[string]any{"model_call_id": "model-1", "prompt_tokens": 8, "completion_tokens": 5, "total_tokens": 13}},
		{Sequence: 5, Type: domain.EventToolStarted, Payload: map[string]any{"tool_call_id": "tool-1"}},
		{Sequence: 6, Type: domain.EventTurnCompleted, TurnID: "turn-1"},
		{Sequence: 7, Type: domain.EventStageCompleted, StageID: "stage-1"},
	}
	ledger := domain.RunUsageLedger{RunID: run.ID, Totals: domain.RunUsageTotals{TotalTokens: 13}}
	evidence := []domain.VerificationEvidence{{ID: "evidence-1", Attempt: 1, SubjectHash: "sha256:subject", Status: domain.VerificationPassed, CompletedAt: completed}}

	first := BuildSnapshot(run, events, ledger, evidence)
	second := BuildSnapshot(run, events, ledger, evidence)
	left, _ := json.Marshal(first)
	right, _ := json.Marshal(second)
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("projection is not deterministic:\n%s\n%s", left, right)
	}
	if first.AsOfSequence != 7 || first.Run.AsOfSequence != 7 || first.Usage.AsOfSequence != 7 || first.Verification.AsOfSequence != 7 {
		t.Fatalf("watermarks differ: %#v", first)
	}
	if first.Run.Summary.TotalDurationMS != 3000 || first.Run.Summary.TotalTokens != 13 || first.Run.Summary.LLMCalls != 1 {
		t.Fatalf("unexpected summary: %#v", first.Run.Summary)
	}
	if len(first.Run.ActiveToolCallIDs) != 1 || first.Run.ActiveToolCallIDs[0] != "tool-1" || len(first.Run.ActiveStageIDs) != 0 {
		t.Fatalf("unexpected active scopes: %#v", first.Run)
	}
	if first.Verification.FreshEvidenceCount != 1 || first.Verification.CurrentSubjectHash != "sha256:subject" {
		t.Fatalf("unexpected verification projection: %#v", first.Verification)
	}
}
