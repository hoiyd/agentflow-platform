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

func TestProjectionCoverageContractRejectsLiveAndUnknownEvents(t *testing.T) {
	if !ConsumesRunEvent(domain.EventRunCreated) || !ConsumesRunEvent(domain.EventBudgetExceeded) {
		t.Fatal("durable projection inputs should be consumed")
	}
	if ConsumesRunEvent(domain.EventModelDelta) || ConsumesRunEvent(domain.RunEventType("unknown.event")) {
		t.Fatal("live and unknown events should not be consumed by the Run projection")
	}
}

func TestBuildRunTraceSummaryCountsFailuresAndNumericPayloads(t *testing.T) {
	started := time.Date(2026, 8, 26, 1, 0, 0, 0, time.UTC)
	updated := started.Add(2 * time.Second)
	run := domain.Run{ID: "run-1", Status: domain.RunFailed, StartedAt: &started, UpdatedAt: updated}
	events := []domain.RunEvent{
		{Type: domain.EventModelCompleted, Payload: map[string]any{
			"prompt_tokens": float64(3), "completion_tokens": int64(2), "total_tokens": json.Number("5"), "token_usage_estimated": true,
		}},
		{Type: domain.EventToolCompleted},
		{Type: domain.EventToolFailed},
		{Type: domain.EventModelFailed},
		{Type: domain.EventBudgetExceeded},
	}
	summary := BuildRunTraceSummary(run, events)
	if summary.TotalDurationMS != 2000 || summary.LLMCalls != 1 || summary.ToolCalls != 2 || summary.ErrorCount != 3 {
		t.Fatalf("unexpected trace totals: %#v", summary)
	}
	if summary.PromptTokens != 3 || summary.CompletionTokens != 2 || summary.TotalTokens != 5 || !summary.TokenUsageEstimated {
		t.Fatalf("unexpected token totals: %#v", summary)
	}
}

func TestBuildVerificationProjectionHandlesEmptyAndSupersededEvidence(t *testing.T) {
	run := domain.Run{ID: "run-1", VerificationStatus: domain.VerificationRunning}
	empty := BuildVerificationProjection(run, nil, 4)
	if empty.CurrentSubjectHash != "" || empty.FreshEvidenceCount != 0 || empty.AsOfSequence != 4 {
		t.Fatalf("unexpected empty projection: %#v", empty)
	}
	now := time.Date(2026, 8, 26, 2, 0, 0, 0, time.UTC)
	evidence := []domain.VerificationEvidence{
		{ID: "old", Attempt: 1, SubjectHash: "old-subject", Status: domain.VerificationPassed, CompletedAt: now},
		{ID: "stale", Attempt: 2, SubjectHash: "new-subject", Status: domain.VerificationStale, SupersedesEvidenceID: "old", CompletedAt: now.Add(time.Second)},
		{ID: "new", Attempt: 2, SubjectHash: "new-subject", Status: domain.VerificationPassed, CompletedAt: now.Add(2 * time.Second)},
	}
	result := BuildVerificationProjection(run, evidence, 7)
	if result.LatestAttempt != 2 || result.CurrentSubjectHash != "new-subject" || result.FreshEvidenceCount != 1 || result.EvidenceCount != 3 {
		t.Fatalf("unexpected superseded evidence projection: %#v", result)
	}
}
