package recovery

import (
	"errors"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func TestMarkStaleRunningRuns(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("Recovery scan")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := fileStore.CreateRun("agent_planner", conversation.ID, domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{},
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := fileStore.UpdateRunStatus(run.ID, domain.RunRunning, ""); err != nil {
		t.Fatalf("mark running: %v", err)
	}
	step, err := fileStore.CreateCollaborationStep(domain.CollaborationStep{
		RunID: run.ID, ConversationID: conversation.ID, Role: "worker",
		Status: domain.CollaborationStepRunning, Input: "continue work",
	})
	if err != nil {
		t.Fatalf("create running step: %v", err)
	}
	for _, item := range []domain.RunEvent{
		{Type: domain.EventStageStarted, RunID: run.ID, ConversationID: conversation.ID, StageID: "stage-1"},
		{Type: domain.EventTurnStarted, RunID: run.ID, ConversationID: conversation.ID, StageID: "stage-1", TurnID: "turn-1"},
		{Type: domain.EventModelStarted, RunID: run.ID, ConversationID: conversation.ID, StageID: "stage-1", TurnID: "turn-1"},
	} {
		if _, err := fileStore.CreateRunEvent(item); err != nil {
			t.Fatalf("create run event: %v", err)
		}
	}
	time.Sleep(time.Millisecond)

	count, err := MarkStaleRunningRuns(fileStore, time.Nanosecond)
	if err != nil {
		t.Fatalf("mark stale running runs: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one recovered run, got %d", count)
	}
	updated, ok, err := fileStore.GetRun(run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if !ok || updated.Status != domain.RunFailedRecoverable {
		t.Fatalf("expected failed_recoverable run, got %#v", updated)
	}
	if updated.Error != staleRunMessage {
		t.Fatalf("expected stale message, got %q", updated.Error)
	}
	events, err := fileStore.ListRunEvents(run.ID)
	if err != nil {
		t.Fatalf("list repaired events: %v", err)
	}
	if len(events) != 6 {
		t.Fatalf("expected three synthetic terminals, got %d events", len(events))
	}
	for _, item := range events[3:] {
		if item.Payload["synthetic"] != true {
			t.Fatalf("expected synthetic terminal event, got %#v", item)
		}
	}
	steps, err := fileStore.ListCollaborationSteps(run.ID)
	if err != nil || len(steps) != 1 || steps[0].ID != step.ID || steps[0].Status != domain.CollaborationStepFailed {
		t.Fatalf("expected interrupted stage record to fail: %#v err=%v", steps, err)
	}

	count, err = MarkStaleRunningRuns(fileStore, time.Nanosecond)
	if err != nil || count != 0 {
		t.Fatalf("expected idempotent second scan, count=%d err=%v", count, err)
	}
}

func TestReconcileChildRunDelegationRebuildsCompletedResult(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := fileStore.CreateConversation("delegation recovery")
	if err != nil {
		t.Fatal(err)
	}
	base := domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, Mode: "single",
		Agent:     domain.RuntimeAgentSnapshot{ID: "agent_planner", Executor: domain.DefaultAgentExecutor},
		Model:     domain.RuntimeModelSnapshot{Provider: "local", Model: "test"},
		RunBudget: &domain.RuntimeRunBudget{}, CreatedAt: time.Now().UTC(),
	}
	parent, err := fileStore.CreateRun("agent_planner", conversation.ID, base)
	if err != nil {
		t.Fatal(err)
	}
	parentStep, err := fileStore.CreateCollaborationStep(domain.CollaborationStep{
		ID: "parent-worker", RunID: parent.ID, ConversationID: conversation.ID,
		Role: "worker", Status: domain.CollaborationStepRunning, Input: "work",
	})
	if err != nil {
		t.Fatal(err)
	}
	childSnapshot := base
	childSnapshot.Delegation = &domain.RuntimeDelegation{
		DelegationID: "delegation-1", ParentRunID: parent.ID, ParentTurnID: "turn-1",
		ParentStageID: parentStep.ID, Depth: 1, IsolatedContext: true,
		TimeoutMS: time.Minute.Milliseconds(), SummaryMaxChars: 32,
	}
	child, relation, err := fileStore.CreateChildRun(domain.ChildRunRequest{
		Delegation: domain.RunDelegation{
			ID: "delegation-1", ParentRunID: parent.ID, ParentTurnID: "turn-1",
			ParentStageID: parentStep.ID, AgentID: "agent_planner", Depth: 1, Task: "work", TimeoutMS: time.Minute.Milliseconds(),
		},
		RuntimeSnapshot: childSnapshot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fileStore.CreateCollaborationStep(domain.CollaborationStep{
		ID: "child-worker", RunID: child.ID, ConversationID: conversation.ID,
		Role: "worker", Status: domain.CollaborationStepCompleted,
		Input: "work", Output: "This is a completed child result that exceeds the parent summary limit.",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fileStore.UpdateRunStatus(child.ID, domain.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}

	count, err := ReconcileChildRunDelegations(fileStore)
	if err != nil || count != 1 {
		t.Fatalf("reconcile count=%d err=%v", count, err)
	}
	updated, ok, err := fileStore.GetRunDelegation(relation.ID)
	if err != nil || !ok {
		t.Fatalf("relation ok=%v err=%v", ok, err)
	}
	if updated.Status != domain.DelegationCompleted || !updated.SummaryTruncated || updated.OutputRef == "" {
		t.Fatalf("reconciled relation = %#v", updated)
	}
	step, err := fileStore.ListCollaborationSteps(parent.ID)
	if err != nil || len(step) != 1 || step[0].Status != domain.CollaborationStepCompleted {
		t.Fatalf("parent step=%#v err=%v", step, err)
	}
	if count, err := ReconcileChildRunDelegations(fileStore); err != nil || count != 0 {
		t.Fatalf("idempotent reconcile count=%d err=%v", count, err)
	}
}

func TestMarkStaleRunningRunsHandlesDisabledAndFailurePaths(t *testing.T) {
	if count, err := MarkStaleRunningRuns(nil, 0); err != nil || count != 0 {
		t.Fatalf("disabled recovery: count=%d err=%v", count, err)
	}
	wantErr := errors.New("store unavailable")
	tests := []struct {
		name  string
		store *recoveryTestStore
	}{
		{name: "list stale", store: &recoveryTestStore{listErr: wantErr}},
		{name: "list events", store: &recoveryTestStore{runs: []domain.Run{{ID: "run-1"}}, eventsErr: wantErr}},
		{name: "invalid lifecycle", store: &recoveryTestStore{
			runs:   []domain.Run{{ID: "run-1"}},
			events: []domain.RunEvent{{Sequence: 2, SchemaVersion: 1, Type: domain.EventRunStarted}},
		}},
		{name: "repair", store: &recoveryTestStore{runs: []domain.Run{{ID: "run-1"}}, repairErr: wantErr}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := MarkStaleRunningRuns(test.store, time.Second); err == nil {
				t.Fatal("expected recovery error")
			}
		})
	}
	notApplied := &recoveryTestStore{runs: []domain.Run{{ID: "run-1"}}}
	if count, err := MarkStaleRunningRuns(notApplied, time.Second); err != nil || count != 0 {
		t.Fatalf("non-applied repair: count=%d err=%v", count, err)
	}
}

type recoveryTestStore struct {
	runs      []domain.Run
	events    []domain.RunEvent
	result    domain.InterruptedRunRepairResult
	listErr   error
	eventsErr error
	repairErr error
}

func (s *recoveryTestStore) ListStaleRunningRuns(time.Time) ([]domain.Run, error) {
	return s.runs, s.listErr
}

func (s *recoveryTestStore) ListRunEvents(string) ([]domain.RunEvent, error) {
	return s.events, s.eventsErr
}

func (s *recoveryTestStore) RepairInterruptedRun(domain.InterruptedRunRepair) (domain.InterruptedRunRepairResult, error) {
	return s.result, s.repairErr
}
