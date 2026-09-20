package projection

import (
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func TestBuildOperatorAttentionUsesRecoveryEvidenceAndCurrentWatermark(t *testing.T) {
	replay := domain.RunReplay{
		Run:                  domain.Run{ID: "run-1", ConversationID: "conversation-1", Status: domain.RunFailedRecoverable, UpdatedAt: time.Unix(10, 0)},
		Conversation:         domain.Conversation{Title: "Repair deployment"},
		Projection:           domain.RunProjectionSnapshot{AsOfSequence: 2},
		RunEvents:            []domain.RunEvent{{Sequence: 3}, {Sequence: 7}},
		ToolEffects:          []domain.ToolEffectSummary{{IdempotencyKey: "effect-1", ToolName: "deploy", Status: domain.ToolEffectNeedsReconciliation}},
		VerificationEvidence: []domain.VerificationEvidence{{ID: "verification-1", Status: domain.VerificationFailed}},
		TaskStateRevisions:   []domain.TaskStateRevision{{Version: 1, State: domain.TaskState{Blockers: []domain.TaskBlocker{{ID: "blocker-1", Status: domain.TaskBlockerOpen}}}}},
	}

	item := BuildOperatorAttention(replay)
	if item == nil || item.Reason != domain.AttentionReconciliationRequired || item.ObservationSequence != 7 {
		t.Fatalf("unexpected attention item: %#v", item)
	}
	if item.ConversationTitle != "Repair deployment" || len(item.Evidence) != 3 || item.Evidence[0].ID != "effect-1" {
		t.Fatalf("missing identity or evidence: %#v", item)
	}
	if item.RecommendedAction == nil || item.RecommendedAction.Kind != "reconcile_tool_effect" || !item.RecommendedAction.Enabled {
		t.Fatalf("unexpected recommended action: %#v", item.RecommendedAction)
	}
}

func TestBuildOperatorAttentionKeepsMissingEvidenceAndDisabledActionVisible(t *testing.T) {
	item := BuildOperatorAttention(domain.RunReplay{Run: domain.Run{
		ID: "run-1", Status: domain.RunWaitingForUser, VerificationStatus: domain.VerificationBlocked,
	}})
	if item == nil || item.Reason != domain.AttentionVerification || len(item.Evidence) != 0 {
		t.Fatalf("unexpected attention item: %#v", item)
	}
	if item.RecommendedAction == nil || item.RecommendedAction.Enabled || item.RecommendedAction.UnavailableReason == "" {
		t.Fatalf("disabled action should explain missing evidence: %#v", item.RecommendedAction)
	}
}

func TestBuildOperatorAttentionListFiltersAndOrdersByStablePriority(t *testing.T) {
	now := time.Unix(20, 0)
	replays := []domain.RunReplay{
		{Run: domain.Run{ID: "completed", Status: domain.RunCompleted, UpdatedAt: now}},
		{Run: domain.Run{ID: "canceled", Status: domain.RunCanceled, UpdatedAt: now}},
		{Run: domain.Run{ID: "failed", Status: domain.RunFailed, UpdatedAt: now}},
		{Run: domain.Run{ID: "verification", Status: domain.RunWaitingForUser, VerificationStatus: domain.VerificationFailed, UpdatedAt: now}},
		{Run: domain.Run{ID: "waiting", Status: domain.RunWaitingForUser, UpdatedAt: now}},
		{Run: domain.Run{ID: "recoverable", Status: domain.RunFailedRecoverable, UpdatedAt: now}},
		{Run: domain.Run{ID: "reconcile", Status: domain.RunFailedRecoverable, UpdatedAt: now}, ToolEffects: []domain.ToolEffectSummary{{Status: domain.ToolEffectNeedsReconciliation}}},
		{Run: domain.Run{ID: "budget", Status: domain.RunFailed, UpdatedAt: now}, RunEvents: []domain.RunEvent{{Type: domain.EventBudgetExceeded, Sequence: 1}}},
	}

	items := BuildOperatorAttentionList(replays)
	want := []string{"reconcile", "recoverable", "waiting", "verification", "budget", "failed"}
	if len(items) != len(want) {
		t.Fatalf("items=%#v", items)
	}
	for index := range want {
		if items[index].RunID != want[index] {
			t.Fatalf("item %d: got %s want %s", index, items[index].RunID, want[index])
		}
	}
}
