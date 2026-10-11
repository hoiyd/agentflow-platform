package store

import (
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
	"testing"
	"time"
)

func TestPostgresDurableRecoveryRoundTrip(t *testing.T) {
	databaseURL := pgfixture.DatabaseURL(t)
	postgresStore, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatalf("new postgres store: %v", err)
	}
	defer postgresStore.Close()
	conversation, err := postgresStore.CreateConversation("Postgres durable recovery")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = postgresStore.DeleteConversation(conversation.ID) })
	run, err := postgresStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := postgresStore.SaveStageCheckpoint(domain.StageCheckpoint{
		Provider: "internal_state_v1", RunID: run.ID, ConversationID: conversation.ID,
		StageID: "stage-1", Status: domain.CheckpointPrepared, InputHash: "input",
		RuntimeSnapshotHash: "snapshot", ToolDefinitionsHash: "tools",
	})
	if err != nil {
		t.Fatal(err)
	}
	if found, ok, err := postgresStore.GetStageCheckpoint(run.ID, checkpoint.StageID); err != nil || !ok || found.ID != checkpoint.ID {
		t.Fatalf("get checkpoint: %#v ok=%v err=%v", found, ok, err)
	}
	if _, ok, err := postgresStore.GetStageCheckpoint(run.ID, "missing"); err != nil || ok {
		t.Fatalf("missing checkpoint: ok=%v err=%v", ok, err)
	}
	checkpoint.Status = domain.CheckpointExecuting
	checkpoint.EventCursor = 1
	if _, err := postgresStore.SaveStageCheckpoint(checkpoint); err != nil {
		t.Fatal(err)
	}
	effect, execute, err := postgresStore.BeginToolEffect(domain.ToolEffectRecord{
		IdempotencyKey: "effect-" + run.ID, RunID: run.ID, StageID: "stage-1",
		ToolCallID: "call-1", ToolName: "write_record", RequestHash: "request",
	})
	if err != nil || !execute {
		t.Fatalf("begin effect: execute=%v effect=%#v err=%v", execute, effect, err)
	}
	if duplicate, execute, err := postgresStore.BeginToolEffect(domain.ToolEffectRecord{
		IdempotencyKey: effect.IdempotencyKey, RunID: run.ID, StageID: "stage-1",
		ToolCallID: "call-1", ToolName: "write_record", RequestHash: "request",
	}); err != nil || execute || duplicate.Status != domain.ToolEffectExecuting {
		t.Fatalf("duplicate effect: execute=%v effect=%#v err=%v", execute, duplicate, err)
	}
	if _, err := postgresStore.CompleteToolEffect(effect.IdempotencyKey, []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := postgresStore.CompleteToolEffect(effect.IdempotencyKey, []byte(`{"ok":true}`)); err != nil {
		t.Fatalf("idempotent effect commit: %v", err)
	}
	if _, err := postgresStore.CompleteToolEffect(effect.IdempotencyKey, []byte(`{"ok":false}`)); err == nil {
		t.Fatal("expected committed result conflict")
	}
	if _, err := postgresStore.MarkToolEffectNeedsReconciliation(effect.IdempotencyKey, "late failure"); err == nil {
		t.Fatal("expected committed effect reconciliation error")
	}
	uncertain, execute, err := postgresStore.BeginToolEffect(domain.ToolEffectRecord{
		IdempotencyKey: "uncertain-" + run.ID, RunID: run.ID, StageID: "stage-1",
		ToolCallID: "call-2", ToolName: "write_record", RequestHash: "request-2",
	})
	if err != nil || !execute {
		t.Fatalf("begin uncertain effect: execute=%v effect=%#v err=%v", execute, uncertain, err)
	}
	uncertain, err = postgresStore.MarkToolEffectNeedsReconciliation(uncertain.IdempotencyKey, "timeout")
	if err != nil || uncertain.Status != domain.ToolEffectNeedsReconciliation {
		t.Fatalf("mark uncertain effect: %#v err=%v", uncertain, err)
	}
	replay, ok, err := postgresStore.GetRunReplay(run.ID)
	if err != nil || !ok || replay.RecoverySummary == nil || replay.RecoverySummary.Reason != domain.RecoveryToolEffectUncertain {
		t.Fatalf("postgres recovery summary: summary=%#v ok=%v err=%v", replay.RecoverySummary, ok, err)
	}
	reconciliation := domain.ToolEffectReconciliation{
		CommandID: "postgres-command", IdempotencyKey: uncertain.IdempotencyKey,
		ExpectedVersion: uncertain.Version, Action: domain.ToolEffectConfirmFailed,
		NextStatus: domain.ToolEffectFailed, Error: "provider confirmed no write",
		Event: domain.RunEvent{
			ID: "event-reconcile-" + run.ID, Type: domain.EventToolEffectReconciled,
			RunID: run.ID, ConversationID: conversation.ID, StageID: uncertain.StageID,
			Payload: map[string]any{
				"command_id": "postgres-command", "idempotency_key": uncertain.IdempotencyKey,
				"action": string(domain.ToolEffectConfirmFailed), "expected_version": uncertain.Version,
			},
		},
	}
	settled, audit, applied, err := postgresStore.CommitToolEffectReconciliation(reconciliation)
	if err != nil || !applied || settled.Status != domain.ToolEffectFailed || settled.Version != uncertain.Version+1 || audit.Sequence == 0 {
		t.Fatalf("reconcile effect: settled=%#v audit=%#v applied=%v err=%v", settled, audit, applied, err)
	}
	if duplicate, _, applied, err := postgresStore.CommitToolEffectReconciliation(reconciliation); err != nil || applied || duplicate.Version != settled.Version {
		t.Fatalf("duplicate reconciliation: effect=%#v applied=%v err=%v", duplicate, applied, err)
	}
	checkpoints, err := postgresStore.ListStageCheckpoints(run.ID)
	if err != nil || len(checkpoints) != 1 || checkpoints[0].Status != domain.CheckpointExecuting {
		t.Fatalf("checkpoint round trip: %#v err=%v", checkpoints, err)
	}
	effects, err := postgresStore.ListToolEffects(run.ID)
	if err != nil || len(effects) != 2 || effects[0].Status != domain.ToolEffectCommitted || effects[1].Status != domain.ToolEffectFailed {
		t.Fatalf("effect round trip: %#v err=%v", effects, err)
	}
}

func TestPostgresInterruptedRunRepairIsAtomicAndIdempotent(t *testing.T) {
	databaseURL := pgfixture.DatabaseURL(t)
	postgresStore, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer postgresStore.Close()
	conversation, err := postgresStore.CreateConversation("Postgres interrupted repair")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = postgresStore.DeleteConversation(conversation.ID) })
	run, err := postgresStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := postgresStore.UpdateRunStatus(run.ID, domain.RunRunning, ""); err != nil {
		t.Fatal(err)
	}
	step, err := postgresStore.CreateCollaborationStep(domain.CollaborationStep{
		RunID: run.ID, ConversationID: conversation.ID, Role: "worker",
		Status: domain.CollaborationStepRunning, Input: "write update",
	})
	if err != nil {
		t.Fatal(err)
	}
	staleEffect, execute, err := postgresStore.BeginToolEffect(domain.ToolEffectRecord{
		IdempotencyKey: "stale-effect-" + run.ID, RunID: run.ID, StageID: step.ID, TurnID: "turn-1",
		ToolCallID: "call-1", ToolName: "external_writer", RequestHash: "request",
	})
	if err != nil || !execute {
		t.Fatalf("begin stale effect: effect=%#v execute=%v err=%v", staleEffect, execute, err)
	}
	for _, item := range []domain.RunEvent{
		{Type: domain.EventRunCreated, RunID: run.ID, ConversationID: conversation.ID},
		{Type: domain.EventRunStarted, RunID: run.ID, ConversationID: conversation.ID},
		{Type: domain.EventStageStarted, RunID: run.ID, ConversationID: conversation.ID, StageID: step.ID},
		{Type: domain.EventTurnStarted, RunID: run.ID, ConversationID: conversation.ID, StageID: step.ID, TurnID: "turn-1"},
		{Type: domain.EventModelStarted, RunID: run.ID, ConversationID: conversation.ID, StageID: step.ID, TurnID: "turn-1"},
		{Type: domain.EventToolStarted, RunID: run.ID, ConversationID: conversation.ID, StageID: step.ID, TurnID: "turn-1", Payload: map[string]any{"tool_call_id": "call-1"}},
	} {
		if _, err := postgresStore.CreateRunEvent(item); err != nil {
			t.Fatal(err)
		}
	}
	events, err := postgresStore.ListRunEvents(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := eventpkg.PlanInterruptedLifecycleRepair(events, domain.InterruptedWorkerReason)
	if err != nil {
		t.Fatal(err)
	}
	request := domain.InterruptedRunRepair{
		RunID: run.ID, StaleBefore: time.Now().UTC().Add(time.Hour),
		ExpectedEventCursor: events[len(events)-1].Sequence, TerminalEvents: terminal,
		ErrorMessage: "worker interrupted",
	}
	terminal = append(terminal, domain.RunEvent{
		Type: domain.EventRunFailed, RunID: run.ID, ConversationID: conversation.ID,
		Payload: map[string]any{"status": string(domain.RunFailedRecoverable), "error": "worker interrupted"},
	})
	request.TerminalEvents = terminal
	result, err := postgresStore.RepairInterruptedRun(request)
	if err != nil || !result.Applied || result.Run.Status != domain.RunFailedRecoverable || len(result.AppendedEvents) != 5 {
		t.Fatalf("repair run: %#v err=%v", result, err)
	}
	repairedEvents, err := postgresStore.ListRunEvents(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := eventpkg.ValidateLifecycle(repairedEvents); err != nil {
		t.Fatalf("validate repaired lifecycle: %v", err)
	}
	steps, err := postgresStore.ListCollaborationSteps(run.ID)
	if err != nil || len(steps) != 1 || steps[0].Status != domain.CollaborationStepFailed {
		t.Fatalf("repaired steps: %#v err=%v", steps, err)
	}
	effects, err := postgresStore.ListToolEffects(run.ID)
	if err != nil || len(effects) != 1 || effects[0].Status != domain.ToolEffectNeedsReconciliation || effects[0].Version != staleEffect.Version+1 {
		t.Fatalf("repaired effects: %#v err=%v", effects, err)
	}
	second, err := postgresStore.RepairInterruptedRun(request)
	if err != nil || second.Applied {
		t.Fatalf("idempotent repair: %#v err=%v", second, err)
	}
}
