package projection

import (
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestBuildRecoverySummaryKeepsNormalRunsQuiet(t *testing.T) {
	for _, status := range []domain.RunStatus{domain.RunQueued, domain.RunRunning, domain.RunCompleted} {
		replay := domain.RunReplay{Run: domain.Run{ID: "run-1", Status: status}}
		if status == domain.RunCompleted {
			replay.TaskStateRevisions = []domain.TaskStateRevision{{Version: 1, State: domain.TaskState{Blockers: []domain.TaskBlocker{{ID: "later-blocker", Description: "belongs to later work", Status: domain.TaskBlockerOpen}}}}}
		}
		if summary := BuildRecoverySummary(replay); summary != nil {
			t.Fatalf("status %s produced recovery summary: %#v", status, summary)
		}
	}
}

func TestBuildRecoverySummaryBlocksResumeForUncertainToolEffect(t *testing.T) {
	replay := domain.RunReplay{
		Run:         domain.Run{ID: "run-1", ConversationID: "conversation-1", Status: domain.RunFailedRecoverable, Error: "worker interrupted"},
		ToolEffects: []domain.ToolEffectSummary{{IdempotencyKey: "effect-1", ToolName: "external_writer", Status: domain.ToolEffectNeedsReconciliation}},
		TaskStateRevisions: []domain.TaskStateRevision{{Version: 2, State: domain.TaskState{
			ArtifactRefs: []string{"artifact://report"},
			Tasks:        []domain.TaskItem{{ID: "task-1", ArtifactRefs: []string{"artifact://report", "artifact://log"}}},
		}}},
	}
	summary := BuildRecoverySummary(replay)
	if summary == nil || summary.Reason != domain.RecoveryToolEffectUncertain {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	if len(summary.Evidence) != 2 || summary.Evidence[1].Kind != "tool_effect" {
		t.Fatalf("unexpected evidence: %#v", summary.Evidence)
	}
	if len(summary.ArtifactRefs) != 2 {
		t.Fatalf("artifact refs should be deduplicated: %#v", summary.ArtifactRefs)
	}
	assertRecoveryAction(t, summary.Actions, "reconcile_tool_effect", true, "")
	assertRecoveryAction(t, summary.Actions, "resume_run", false, "Resolve uncertain tool effects before resuming")
}

func TestBuildRecoverySummaryDistinguishesParentAndChildRecovery(t *testing.T) {
	child := domain.RunReplay{
		Run:              domain.Run{ID: "child", Status: domain.RunFailedRecoverable},
		ParentDelegation: &domain.RunDelegation{ParentRunID: "parent", ChildRunID: "child", Status: domain.DelegationBlocked},
	}
	childSummary := BuildRecoverySummary(child)
	if childSummary == nil || childSummary.Reason != domain.RecoveryChildOwnedByParent {
		t.Fatalf("child summary: %#v", childSummary)
	}
	assertRecoveryAction(t, childSummary.Actions, "inspect_parent_run", true, "")
	if hasRecoveryAction(childSummary.Actions, "resume_run") {
		t.Fatalf("child replay must not expose resume: %#v", childSummary.Actions)
	}

	parent := domain.RunReplay{
		Run:              domain.Run{ID: "parent", Status: domain.RunFailedRecoverable},
		ChildDelegations: []domain.RunDelegation{{ChildRunID: "child", Status: domain.DelegationBlocked, Error: "child heartbeat expired", OutputRef: "run://child/stages/worker"}},
	}
	parentSummary := BuildRecoverySummary(parent)
	if parentSummary == nil || parentSummary.Reason != domain.RecoveryChildBlocked {
		t.Fatalf("parent summary: %#v", parentSummary)
	}
	assertRecoveryAction(t, parentSummary.Actions, "resume_run", true, "")
	assertRecoveryAction(t, parentSummary.Actions, "inspect_child_run", true, "")
	if len(parentSummary.ArtifactRefs) != 1 || parentSummary.ArtifactRefs[0] != "run://child/stages/worker" {
		t.Fatalf("parent output refs: %#v", parentSummary.ArtifactRefs)
	}
}

func TestBuildRecoverySummaryExplainsVerificationTaskAndTerminalStates(t *testing.T) {
	tests := []struct {
		name   string
		replay domain.RunReplay
		reason domain.RecoveryReason
	}{
		{name: "verification without evidence", replay: domain.RunReplay{Run: domain.Run{Status: domain.RunWaitingForUser, VerificationStatus: domain.VerificationBlocked}}, reason: domain.RecoveryVerificationBlocked},
		{name: "open task blocker", replay: domain.RunReplay{Run: domain.Run{Status: domain.RunWaitingForUser}, TaskStateRevisions: []domain.TaskStateRevision{{Version: 1, State: domain.TaskState{Blockers: []domain.TaskBlocker{{ID: "blocker-1", Description: "Missing approval", Status: domain.TaskBlockerOpen}}}}}}, reason: domain.RecoveryTaskBlocked},
		{name: "waiting", replay: domain.RunReplay{Run: domain.Run{Status: domain.RunWaitingForUser}}, reason: domain.RecoveryInputRequired},
		{name: "failed", replay: domain.RunReplay{Run: domain.Run{Status: domain.RunFailed}}, reason: domain.RecoveryRunFailed},
		{name: "canceled", replay: domain.RunReplay{Run: domain.Run{Status: domain.RunCanceled}}, reason: domain.RecoveryRunCanceled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			summary := BuildRecoverySummary(test.replay)
			if summary == nil || summary.Reason != test.reason {
				t.Fatalf("unexpected summary: %#v", summary)
			}
			if test.reason == domain.RecoveryRunFailed || test.reason == domain.RecoveryRunCanceled {
				if hasRecoveryAction(summary.Actions, "resume_run") {
					t.Fatalf("terminal run exposed resume: %#v", summary.Actions)
				}
			}
		})
	}

	verification := BuildRecoverySummary(tests[0].replay)
	assertRecoveryAction(t, verification.Actions, "review_verification", false, "No verification evidence was recorded")
	blocked := BuildRecoverySummary(tests[1].replay)
	assertRecoveryAction(t, blocked.Actions, "review_task_state", true, "")
}

func assertRecoveryAction(t *testing.T, actions []domain.RecoveryAction, kind string, enabled bool, reason string) {
	t.Helper()
	for _, action := range actions {
		if action.Kind == kind {
			if action.Enabled != enabled || action.UnavailableReason != reason {
				t.Fatalf("action %s: %#v", kind, action)
			}
			return
		}
	}
	t.Fatalf("missing action %s in %#v", kind, actions)
}

func hasRecoveryAction(actions []domain.RecoveryAction, kind string) bool {
	for _, action := range actions {
		if action.Kind == kind {
			return true
		}
	}
	return false
}
