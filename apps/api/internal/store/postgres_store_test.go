package store

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
)

func TestPostgresMigrationsUpgradeLegacyRunUsageEntries(t *testing.T) {
	joined := strings.Join(postgresMigrations, "\n")
	for _, expected := range []string{
		"ADD COLUMN IF NOT EXISTS operation_id text",
		"SET operation_id = id",
		"ALTER COLUMN operation_id SET NOT NULL",
		"ADD COLUMN IF NOT EXISTS purpose text",
		"SET purpose = 'primary'",
		"ALTER COLUMN purpose SET NOT NULL",
		"ADD COLUMN IF NOT EXISTS model text",
		"SET model = '' WHERE model IS NULL",
		"ADD COLUMN IF NOT EXISTS tool_name text",
		"SET tool_name = '' WHERE tool_name IS NULL",
		"run_usage_entries_run_operation_kind_idx",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing legacy run usage migration step %q", expected)
		}
	}
}

func TestPostgresMigrationsAddDurableRecoveryState(t *testing.T) {
	joined := strings.Join(postgresMigrations, "\n")
	for _, expected := range []string{
		"CREATE TABLE IF NOT EXISTS stage_checkpoints",
		"UNIQUE(run_id, stage_id)",
		"stage_checkpoints_run_cursor_idx",
		"CREATE TABLE IF NOT EXISTS tool_effects",
		"idempotency_key text PRIMARY KEY",
		"tool_effects_run_stage_idx",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing durable recovery migration step %q", expected)
		}
	}
}

func TestPostgresMigrationsAddRunDelegations(t *testing.T) {
	joined := strings.Join(postgresMigrations, "\n")
	for _, expected := range []string{
		"CREATE TABLE IF NOT EXISTS run_delegations",
		"parent_run_id text NOT NULL REFERENCES runs(id)",
		"child_run_id text NOT NULL UNIQUE REFERENCES runs(id)",
		"block_reason text NOT NULL DEFAULT ''",
		"idx_run_delegations_parent_created",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing run delegation migration step %q", expected)
		}
	}
}

func TestPostgresRunDelegationRoundTrip(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	postgresStore, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer postgresStore.Close()
	conversation, err := postgresStore.CreateConversationInWorkspace("workspace-delegation", "Postgres delegation")
	if err != nil {
		t.Fatal(err)
	}
	base := testRuntimeSnapshot()
	base.Mode = "single"
	base.AutonomousLimits = nil
	parent, err := postgresStore.CreateRunWithContract("agent_planner", conversation.ID, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	childSnapshot := base
	childSnapshot.Delegation = &domain.RuntimeDelegation{
		DelegationID: "delegation-postgres-" + parent.ID, ParentRunID: parent.ID,
		ParentTurnID: "turn-postgres", Depth: 1, IsolatedContext: true,
		TimeoutMS: time.Minute.Milliseconds(), SummaryMaxChars: 100,
	}
	request := domain.ChildRunRequest{
		Delegation: domain.RunDelegation{
			ID: childSnapshot.Delegation.DelegationID, ParentRunID: parent.ID,
			ParentTurnID: "turn-postgres", AgentID: "agent_planner", Depth: 1, Task: "work", TimeoutMS: time.Minute.Milliseconds(),
		},
		RuntimeSnapshot: childSnapshot,
	}
	child, relation, err := postgresStore.CreateChildRun(request)
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := postgresStore.UpdateRunDelegation(relation.ID, domain.DelegationResult{
		Status: domain.DelegationBlocked, BlockReason: domain.DelegationBlockReasonChildRecoveryRequired,
	})
	if err != nil || blocked.BlockReason != domain.DelegationBlockReasonChildRecoveryRequired {
		t.Fatalf("blocked postgres delegation=%#v err=%v", blocked, err)
	}
	updated, err := postgresStore.UpdateRunDelegation(relation.ID, domain.DelegationResult{
		Status: domain.DelegationCompleted, Summary: "done", OutputRef: "run://" + child.ID + "/stages/worker",
		OutputHash: "hash", OutputBytes: 4,
	})
	if err != nil || updated.ChildRunID != child.ID {
		t.Fatalf("updated=%#v err=%v", updated, err)
	}
	parentReplay, ok, err := postgresStore.GetRunReplay(parent.ID)
	if err != nil || !ok || len(parentReplay.ChildDelegations) != 1 {
		t.Fatalf("parent replay=%#v ok=%v err=%v", parentReplay.ChildDelegations, ok, err)
	}
	childReplay, ok, err := postgresStore.GetRunReplay(child.ID)
	if err != nil || !ok || childReplay.ParentDelegation == nil || childReplay.ParentDelegation.ID != relation.ID {
		t.Fatalf("child replay parent=%#v ok=%v err=%v", childReplay.ParentDelegation, ok, err)
	}
	if item, ok, err := postgresStore.GetRunDelegation(relation.ID); err != nil || !ok || item.ChildRunID != child.ID {
		t.Fatalf("get delegation=%#v ok=%v err=%v", item, ok, err)
	}
	if item, ok, err := postgresStore.GetParentDelegation(child.ID); err != nil || !ok || item.ID != relation.ID {
		t.Fatalf("get parent delegation=%#v ok=%v err=%v", item, ok, err)
	}
	if _, ok, err := postgresStore.GetRunDelegation("missing"); err != nil || ok {
		t.Fatalf("missing delegation: ok=%v err=%v", ok, err)
	}
	if _, ok, err := postgresStore.GetParentDelegation("missing"); err != nil || ok {
		t.Fatalf("missing parent delegation: ok=%v err=%v", ok, err)
	}
	items, err := postgresStore.ListRunDelegations(parent.ID)
	if err != nil || len(items) != 1 || items[0].ID != relation.ID {
		t.Fatalf("list delegations=%#v err=%v", items, err)
	}
	active, err := postgresStore.ListActiveRunDelegations()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range active {
		if item.ID == relation.ID {
			t.Fatal("completed delegation remained active")
		}
	}

	second := validChildRunRequest(parent, "delegation-postgres-active-"+parent.ID)
	secondChild, secondRelation, err := postgresStore.CreateChildRun(second)
	if err != nil {
		t.Fatal(err)
	}
	active, err = postgresStore.ListActiveRunDelegations()
	if err != nil || !containsDelegation(active, secondRelation.ID) {
		t.Fatalf("active delegations=%#v err=%v", active, err)
	}
	if _, err := postgresStore.UpdateRunDelegation(secondRelation.ID, domain.DelegationResult{Status: domain.DelegationRunning}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := postgresStore.CreateChildRun(second); err == nil {
		t.Fatal("expected duplicate delegation transaction to roll back")
	}
	if _, ok, err := postgresStore.GetRun(secondChild.ID); err != nil || !ok {
		t.Fatalf("duplicate rollback damaged existing child: ok=%v err=%v", ok, err)
	}
	if _, err := postgresStore.UpdateRunDelegation("missing", domain.DelegationResult{Status: domain.DelegationRunning}); err == nil {
		t.Fatal("expected missing delegation update to fail")
	}
	if _, err := postgresStore.UpdateRunDelegation(secondRelation.ID, domain.DelegationResult{}); err == nil {
		t.Fatal("expected invalid delegation result to fail")
	}
	if _, _, err := postgresStore.CreateChildRun(domain.ChildRunRequest{}); err == nil {
		t.Fatal("expected invalid child request to fail")
	}
	missingParent := validChildRunRequest(domain.Run{ID: "missing-parent"}, "delegation-postgres-missing-parent-"+parent.ID)
	if _, _, err := postgresStore.CreateChildRun(missingParent); err == nil {
		t.Fatal("expected missing parent to fail")
	}
	missingAgent := validChildRunRequest(parent, "delegation-postgres-missing-agent-"+parent.ID)
	missingAgent.Delegation.AgentID = "missing-agent"
	missingAgent.RuntimeSnapshot.Agent.ID = "missing-agent"
	if _, _, err := postgresStore.CreateChildRun(missingAgent); err == nil {
		t.Fatal("expected missing agent to fail")
	}
}

func containsDelegation(items []domain.RunDelegation, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func TestPostgresMigrationsAddStructuredTaskState(t *testing.T) {
	joined := strings.Join(postgresMigrations, "\n")
	for _, expected := range []string{
		"CREATE TABLE IF NOT EXISTS task_state_revisions",
		"UNIQUE(conversation_id, version)",
		"task_state_revisions_conversation_version_idx",
		"patch jsonb NOT NULL",
		"state jsonb NOT NULL",
		"source jsonb NOT NULL",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing structured task state migration step %q", expected)
		}
	}
}

func TestPostgresStructuredTaskStateRoundTrip(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	postgresStore, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatalf("new postgres store: %v", err)
	}
	defer postgresStore.Close()
	conversation, err := postgresStore.CreateConversationInWorkspace("workspace-task-state", "Postgres task state")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = postgresStore.DeleteConversation(conversation.ID) })
	run, err := postgresStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := postgresStore.ApplyTaskStatePatch(conversation.ID, domain.TaskStatePatch{ExpectedVersion: 0, Operations: []domain.TaskStateOperation{
		{Type: domain.TaskStateSetGoal, Goal: "Persist exact task facts"},
		{Type: domain.TaskStateUpsertConstraint, Constraint: &domain.TaskConstraint{ID: "scope", Statement: "Stay workspace scoped"}},
	}}, domain.TaskStateSource{ActorType: "model", RunID: run.ID})
	if err != nil || first.Version != 1 || first.State.WorkspaceID != "workspace-task-state" {
		t.Fatalf("first postgres revision: revision=%#v err=%v", first, err)
	}
	if _, err := postgresStore.ApplyTaskStatePatch(conversation.ID, domain.TaskStatePatch{ExpectedVersion: 0, Operations: []domain.TaskStateOperation{{Type: domain.TaskStateClearGoal}}}, domain.TaskStateSource{ActorType: "user"}); !IsTaskStateVersionConflict(err) {
		t.Fatalf("expected postgres version conflict, got %v", err)
	}
	second, err := postgresStore.ApplyTaskStatePatch(conversation.ID, domain.TaskStatePatch{ExpectedVersion: 1, Operations: []domain.TaskStateOperation{
		{Type: domain.TaskStateUpsertTask, Task: &domain.TaskItem{ID: "tests", Title: "Verify Postgres", Status: domain.TaskItemCompleted}},
	}}, domain.TaskStateSource{ActorType: "user"})
	if err != nil || second.Version != 2 {
		t.Fatalf("second postgres revision: revision=%#v err=%v", second, err)
	}
	state, ok, err := postgresStore.GetTaskState(conversation.ID)
	if err != nil || !ok || state.Version != 2 || len(state.Tasks) != 1 {
		t.Fatalf("postgres current task state: state=%#v ok=%v err=%v", state, ok, err)
	}
	historical, ok, err := postgresStore.GetTaskStateRevision(conversation.ID, 1)
	if err != nil || !ok || historical.State.Goal == "" {
		t.Fatalf("postgres historical task state: revision=%#v ok=%v err=%v", historical, ok, err)
	}
	replay, ok, err := postgresStore.GetRunReplay(run.ID)
	if err != nil || !ok || len(replay.TaskStateRevisions) != 2 {
		t.Fatalf("postgres task state replay: revisions=%#v ok=%v err=%v", replay.TaskStateRevisions, ok, err)
	}
}

func TestPostgresDurableRecoveryRoundTrip(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
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
	checkpoints, err := postgresStore.ListStageCheckpoints(run.ID)
	if err != nil || len(checkpoints) != 1 || checkpoints[0].Status != domain.CheckpointExecuting {
		t.Fatalf("checkpoint round trip: %#v err=%v", checkpoints, err)
	}
	effects, err := postgresStore.ListToolEffects(run.ID)
	if err != nil || len(effects) != 2 || effects[0].Status != domain.ToolEffectCommitted || effects[1].Status != domain.ToolEffectNeedsReconciliation {
		t.Fatalf("effect round trip: %#v err=%v", effects, err)
	}
}

func TestPostgresInterruptedRunRepairIsAtomicAndIdempotent(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
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
	for _, item := range []domain.RunEvent{
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
	result, err := postgresStore.RepairInterruptedRun(request)
	if err != nil || !result.Applied || result.Run.Status != domain.RunFailedRecoverable || len(result.AppendedEvents) != 4 {
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
	second, err := postgresStore.RepairInterruptedRun(request)
	if err != nil || second.Applied {
		t.Fatalf("idempotent repair: %#v err=%v", second, err)
	}
}

func TestPostgresMigrationsRepairLegacyVectorAndConfidenceSchema(t *testing.T) {
	joined := strings.Join(postgresMigrations, "\n")
	for _, expected := range []string{
		"memory_candidates ADD COLUMN IF NOT EXISTS confidence",
		"table_name = 'memory_embeddings'",
		"table_name = 'document_chunk_embeddings'",
		"ALTER COLUMN embedding TYPE vector(1536)",
		"USING embedding::vector(1536)",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing legacy vector migration step %q", expected)
		}
	}
}

func TestPostgresRequiredSchemaCoversRuntimeColumns(t *testing.T) {
	columns := map[string]bool{}
	for _, requirement := range postgresRequiredColumns {
		columns[requirement.Table+"."+requirement.Column] = true
	}
	for _, expected := range []string{
		"conversations.workspace_id",
		"messages.workspace_id",
		"messages.citations",
		"runs.workspace_id",
		"stage_checkpoints.provider",
		"stage_checkpoints.status",
		"stage_checkpoints.event_cursor",
		"tool_effects.idempotency_key",
		"tool_effects.status",
		"tool_effects.result",
		"memory_candidates.confidence",
		"memory_embeddings.embedding",
		"documents.workspace_id",
		"documents.lexical_vector",
		"document_chunks.parent_id",
		"document_chunk_embeddings.embedding",
		"run_usage_entries.operation_id",
		"run_usage_entries.purpose",
		"model_request_records.model_call_id",
		"model_request_records.payload_hash",
		"model_request_records.parameters",
		"model_request_records.source_token_breakdown",
		"model_request_records.capture_content",
		"model_request_records.capture_expired",
		"task_state_revisions.workspace_id",
		"task_state_revisions.conversation_id",
		"task_state_revisions.version",
		"task_state_revisions.patch",
		"task_state_revisions.state",
		"task_state_revisions.source",
	} {
		if !columns[expected] {
			t.Fatalf("required schema does not validate %s", expected)
		}
	}
}

func TestPostgresMigrationsAddModelRequestRecords(t *testing.T) {
	joined := strings.Join(postgresMigrations, "\n")
	for _, expected := range []string{
		"CREATE TABLE IF NOT EXISTS model_request_records",
		"UNIQUE(run_id, model_call_id, attempt)",
		"model_request_records_run_created_idx",
		"capture_reconstructable boolean",
		"source_token_breakdown jsonb",
		"capture_expires_at timestamptz",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing model request migration step %q", expected)
		}
	}
}

func TestPostgresMigrationsAddDocumentSourceTraceability(t *testing.T) {
	joined := strings.Join(postgresMigrations, "\n")
	for _, expected := range []string{
		"documents ADD COLUMN IF NOT EXISTS version",
		"documents ADD COLUMN IF NOT EXISTS content_hash",
		"document_chunks ADD COLUMN IF NOT EXISTS parent_id",
		"document_chunks ADD COLUMN IF NOT EXISTS section_path",
		"document_chunks ADD COLUMN IF NOT EXISTS start_offset",
		"document_chunks ADD COLUMN IF NOT EXISTS end_offset",
		"document_chunks ADD COLUMN IF NOT EXISTS document_version",
		"document_chunks ADD COLUMN IF NOT EXISTS content_hash",
		"idx_document_chunks_parent_index",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing document source migration step %q", expected)
		}
	}
}

func TestPostgresMigrationsAddMessageCitations(t *testing.T) {
	joined := strings.Join(postgresMigrations, "\n")
	for _, expected := range []string{
		"citations jsonb NOT NULL DEFAULT '[]'::jsonb",
		"messages ADD COLUMN IF NOT EXISTS citations",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing message citation migration step %q", expected)
		}
	}
}

func TestPostgresMigrationsIndexConversationEventHistory(t *testing.T) {
	joined := strings.Join(postgresMigrations, "\n")
	if !strings.Contains(joined, "idx_run_events_conversation_timestamp") {
		t.Fatal("missing source-aware session history event index")
	}
}

func TestPostgresMigrationsBackfillAndRequireWorkspaceScope(t *testing.T) {
	joined := strings.Join(postgresMigrations, "\n")
	for _, expected := range []string{
		"UPDATE conversations SET workspace_id = 'default_workspace'",
		"UPDATE messages m SET workspace_id",
		"UPDATE runs r SET workspace_id",
		"UPDATE memories SET workspace_id = 'default_workspace'",
		"UPDATE documents SET workspace_id = 'default_workspace'",
		"conversations ALTER COLUMN workspace_id SET NOT NULL",
		"messages ALTER COLUMN workspace_id SET NOT NULL",
		"runs ALTER COLUMN workspace_id SET NOT NULL",
		"memories ALTER COLUMN workspace_id SET NOT NULL",
		"documents ALTER COLUMN workspace_id SET NOT NULL",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing workspace migration step %q", expected)
		}
	}
}

func TestPostgresRunUsageReservationIsAtomic(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatalf("new postgres store: %v", err)
	}
	defer store.Close()
	conversation, err := store.CreateConversation("Postgres usage concurrency")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	t.Cleanup(func() { _ = store.DeleteConversation(conversation.ID) })
	snapshot := testRuntimeSnapshot()
	snapshot.RunBudget = &domain.RuntimeRunBudget{MaxToolCalls: 1}
	run, err := store.CreateRunWithContract("agent_planner", conversation.ID, snapshot, nil)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			_, _, applyErr := store.ApplyRunUsage(domain.RunUsageEntry{
				ID: "usage-tool-" + run.ID + "-" + string(rune('a'+index)), RunID: run.ID,
				OperationID: "tool-call-" + string(rune('a'+index)), Kind: domain.UsageToolExecution,
				Purpose: domain.UsagePurposePrimary, ToolName: "calculator", ToolCalls: 1,
				Timestamp: time.Now().UTC(),
			})
			errs <- applyErr
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	succeeded, rejected := 0, 0
	for applyErr := range errs {
		if applyErr == nil {
			succeeded++
			continue
		}
		if exceeded, ok := budget.AsExceeded(applyErr); ok && exceeded.Resource == budget.ResourceToolCalls {
			rejected++
			continue
		}
		t.Fatalf("unexpected apply error: %v", applyErr)
	}
	ledger, ok, err := store.GetRunUsageLedger(run.ID)
	if err != nil || !ok || succeeded != 1 || rejected != 1 || ledger.Totals.ToolCalls != 1 || len(ledger.Entries) != 1 {
		t.Fatalf("atomic tool reservation failed: succeeded=%d rejected=%d ledger=%#v err=%v", succeeded, rejected, ledger, err)
	}
}

func TestPostgresActiveRuntimeExcludesWaitingForUser(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatalf("new postgres store: %v", err)
	}
	defer store.Close()
	conversation, err := store.CreateConversation("Postgres runtime segments")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	t.Cleanup(func() { _ = store.DeleteConversation(conversation.ID) })
	run, err := store.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	emptyRequests, err := store.ListModelRequestRecords(run.ID)
	if err != nil || len(emptyRequests) != 0 {
		t.Fatalf("list empty model requests: items=%#v err=%v", emptyRequests, err)
	}
	if _, err := store.ListModelRequestRecords("missing-run"); err == nil {
		t.Fatal("expected missing run model request error")
	}
	if _, err = store.UpdateRunStatus(run.ID, domain.RunRunning, ""); err != nil {
		t.Fatalf("start run: %v", err)
	}
	time.Sleep(8 * time.Millisecond)
	waiting, err := store.UpdateRunStatus(run.ID, domain.RunWaitingForUser, "")
	if err != nil || waiting.ActiveRuntimeMS <= 0 || waiting.ExecutionStartedAt != nil {
		t.Fatalf("pause run: run=%#v err=%v", waiting, err)
	}
	activeBeforeWait := waiting.ActiveRuntimeMS
	time.Sleep(20 * time.Millisecond)
	resumed, err := store.UpdateRunStatus(run.ID, domain.RunRunning, "")
	if err != nil || resumed.ActiveRuntimeMS != activeBeforeWait || resumed.ExecutionStartedAt == nil {
		t.Fatalf("waiting time leaked into postgres active runtime: before=%d resumed=%#v err=%v", activeBeforeWait, resumed, err)
	}
}

func TestPostgresReplayKeepsHistoricalRuntimeSnapshotReadable(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	postgresStore, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatalf("new postgres store: %v", err)
	}
	defer postgresStore.Close()
	conversation, err := postgresStore.CreateConversation("Postgres historical snapshot replay")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	t.Cleanup(func() { _ = postgresStore.DeleteConversation(conversation.ID) })
	run, err := postgresStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := postgresStore.db.Exec(
		`UPDATE runs SET runtime_snapshot = jsonb_set(runtime_snapshot, '{schema_version}', to_jsonb($1::int)) WHERE id = $2`,
		domain.LegacyRuntimeSnapshotVersion,
		run.ID,
	); err != nil {
		t.Fatalf("persist historical fixture: %v", err)
	}

	replay, ok, err := postgresStore.GetRunReplay(run.ID)
	if err != nil || !ok {
		t.Fatalf("get historical replay: ok=%v err=%v", ok, err)
	}
	if replay.RuntimeSnapshot == nil || replay.RuntimeSnapshot.SchemaVersion != domain.LegacyRuntimeSnapshotVersion {
		t.Fatalf("historical snapshot was not preserved: %#v", replay.RuntimeSnapshot)
	}
}

func TestPostgresStoreTraceReplay(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatalf("new postgres store: %v", err)
	}
	defer store.Close()

	conversation, err := store.CreateConversation("Postgres trace test")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	t.Cleanup(func() {
		_ = store.DeleteConversation(conversation.ID)
	})

	message, err := store.AddMessage(conversation.ID, "user", "hello")
	if err != nil {
		t.Fatalf("add message: %v", err)
	}
	if _, err := store.AddMessageWithCitations(conversation.ID, "assistant", "Answer [S1].", []domain.RAGCitation{{
		SourceID: "S1", DocumentID: "doc-1", DocumentTitle: "Guide", ChunkID: "chunk-1",
	}}); err != nil {
		t.Fatalf("add cited message: %v", err)
	}
	run, err := store.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	contract := &domain.CompletionContract{
		ID: "contract_" + run.ID, Version: domain.CurrentCompletionContractVersion, Hash: "sha256:contract", SubjectType: "run_output",
		Verifiers: []domain.VerifierSpec{{ID: "schema", Type: domain.VerifierJSONSchema, Version: "1", Required: true,
			Config: map[string]any{"schema": map[string]any{"type": "object"}}}},
		Policy: domain.VerificationPolicy{Mode: domain.VerificationAllMustPass, MaxAttempts: 1, OnExhausted: domain.VerificationFailRun},
	}
	contractRun, err := store.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), contract)
	if err != nil {
		t.Fatalf("create contract run: %v", err)
	}
	now := time.Now().UTC()
	record := domain.VerificationRecord{
		Evidence: domain.VerificationEvidence{
			ID: "evidence_" + contractRun.ID, RunID: contractRun.ID, ContractID: contract.ID,
			ContractVersion: domain.CurrentCompletionContractVersion, VerifierID: "schema", VerifierType: domain.VerifierJSONSchema,
			VerifierVersion: "1", Attempt: 1, SubjectHash: "sha256:subject", SnapshotHash: "sha256:snapshot",
			Status: domain.VerificationPassed, StartedAt: now, CompletedAt: now, Summary: "matched", Details: map[string]any{"matched": true},
			ArtifactIDs: []string{"artifact_" + contractRun.ID},
		},
		Artifacts: []domain.VerificationArtifact{{
			ID: "artifact_" + contractRun.ID, RunID: contractRun.ID, EvidenceID: "evidence_" + contractRun.ID,
			Kind: "verifier_output", MediaType: "text/plain", Content: "matched", ContentHash: "sha256:output",
			ByteSize: 7, CreatedAt: now,
		}},
	}
	if err := store.AppendVerificationRecord(record); err != nil {
		t.Fatalf("append verification record: %v", err)
	}
	if _, err := store.UpdateRunVerificationStatus(contractRun.ID, domain.VerificationPassed); err != nil {
		t.Fatalf("update verification status: %v", err)
	}
	verificationReplay, ok, err := store.GetRunReplay(contractRun.ID)
	if err != nil || !ok || verificationReplay.Run.CompletionContract == nil || len(verificationReplay.VerificationEvidence) != 1 || len(verificationReplay.VerificationArtifacts) != 1 || verificationReplay.VerificationEvidence[0].Details["matched"] != true {
		t.Fatalf("postgres verification round trip: ok=%v err=%v replay=%#v", ok, err, verificationReplay)
	}
	run, err = store.UpdateRunStatus(run.ID, domain.RunRunning, "")
	if err != nil {
		t.Fatalf("mark running: %v", err)
	}
	candidate, created, err := store.CreateMemoryCandidate(domain.MemoryCandidate{
		ID: "memcand_" + message.ID, ConversationID: conversation.ID, RunID: run.ID,
		SourceMessageID: message.ID, SourceRole: "user", Kind: "fact", Content: "hello",
		Status: domain.MemoryCandidateAccepted, ExtractionReason: "adaptive_model", PolicyReason: "accepted", Confidence: 0.92,
	})
	if err != nil || !created {
		t.Fatalf("create memory candidate: candidate=%#v created=%v err=%v", candidate, created, err)
	}
	if _, created, err := store.CreateMemoryCandidate(candidate); err != nil || created {
		t.Fatalf("duplicate candidate should be idempotent: created=%v err=%v", created, err)
	}
	candidates, err := store.ListMemoryCandidates(conversation.ID)
	if err != nil || len(candidates) != 1 || candidates[0].ID != candidate.ID || candidates[0].Confidence != candidate.Confidence {
		t.Fatalf("postgres memory candidate round trip: candidates=%#v err=%v", candidates, err)
	}
	atomicCompactionID := "cmp-postgres-atomic-" + run.ID
	started, err := store.CreateRunEvent(domain.RunEvent{
		Type: domain.EventCompactionStarted, RunID: run.ID, ConversationID: conversation.ID,
		Payload: map[string]any{"compaction_id": atomicCompactionID, "trigger": "hard", "status": "running", "algorithm_version": "v2"},
	})
	if err != nil {
		t.Fatalf("create postgres compaction start: %v", err)
	}
	committed, terminal, err := store.CommitContextCompaction(domain.ContextCompaction{
		ID: atomicCompactionID, ConversationID: conversation.ID, RunID: run.ID, Trigger: "hard",
		Generation: 2, Summary: "atomic summary", SourceMessageIDs: []string{"message-1"},
		SourceEventIDs: []string{started.ID}, ShadowedMessageRange: domain.ContextShadowedRange{FirstMessageID: "message-1", LastMessageID: "message-1", MessageCount: 1},
		SourceHash: "source-hash-atomic-" + run.ID, BeforeTokens: 120, AfterTokens: 30,
		TargetSummaryTokens: 24, ReductionRatio: 0.75, SummaryModel: "test", AlgorithmVersion: "v2",
	}, domain.RunEvent{
		Type: domain.EventCompactionCompleted, RunID: run.ID, ConversationID: conversation.ID,
		Payload: map[string]any{"compaction_id": atomicCompactionID, "trigger": "hard", "status": "completed", "algorithm_version": "v2"},
	})
	if err != nil || terminal.Sequence != started.Sequence+1 || committed.Generation != 2 || len(committed.SourceEventIDs) != 1 {
		t.Fatalf("postgres atomic compaction commit: compaction=%#v terminal=%#v err=%v", committed, terminal, err)
	}
	latest, ok, err := store.GetLatestContextCompaction(conversation.ID)
	if err != nil || !ok || latest.ID != committed.ID {
		t.Fatalf("postgres compaction round trip: ok=%v err=%v item=%#v", ok, err, latest)
	}
	duplicate, duplicateTerminal, err := store.CommitContextCompaction(committed, domain.RunEvent{
		Type: domain.EventCompactionCompleted, RunID: run.ID, ConversationID: conversation.ID,
		Payload: map[string]any{"compaction_id": atomicCompactionID, "trigger": "hard", "status": "completed", "algorithm_version": "v2"},
	})
	if err != nil || duplicate.ID != committed.ID || duplicateTerminal.ID != "" {
		t.Fatalf("postgres duplicate compaction was not idempotent: compaction=%#v terminal=%#v err=%v", duplicate, duplicateTerminal, err)
	}
	generationConflict := committed
	generationConflict.ID += "-conflict"
	generationConflict.SourceHash += "-conflict"
	generationConflict.ReplacementSummaryID = ""
	if _, _, err := store.CommitContextCompaction(generationConflict, domain.RunEvent{
		Type: domain.EventCompactionCompleted, RunID: run.ID, ConversationID: conversation.ID,
		Payload: map[string]any{"compaction_id": generationConflict.ID, "trigger": "hard", "status": "completed", "algorithm_version": "v2"},
	}); err == nil {
		t.Fatal("duplicate compaction generation should fail")
	}
	step, err := store.CreateCollaborationStep(domain.CollaborationStep{
		RunID:          run.ID,
		ConversationID: conversation.ID,
		Role:           "planner",
		Status:         domain.CollaborationStepCompleted,
		Input:          "input",
		Output:         "output",
	})
	if err != nil {
		t.Fatalf("create step: %v", err)
	}
	modelEvent, err := store.CreateRunEvent(domain.RunEvent{
		RunID:          run.ID,
		ConversationID: conversation.ID,
		StageID:        step.ID,
		TurnID:         "turn-postgres-trace",
		ParentEventID:  "event-parent",
		Type:           domain.EventModelCompleted,
		Payload: map[string]any{
			"prompt_tokens":         10,
			"completion_tokens":     5,
			"total_tokens":          15,
			"token_usage_estimated": true,
		},
	})
	if err != nil {
		t.Fatalf("create llm trace: %v", err)
	}
	if _, err := store.CreateRunEvent(domain.RunEvent{
		RunID: run.ID, StageID: step.ID, Type: domain.EventContextAssembled,
		Payload: map[string]any{"manifest": map[string]any{"id": "ctx-1", "assembler_version": "context-assembler-v1", "entries": []any{}}},
	}); err != nil {
		t.Fatalf("create context manifest trace: %v", err)
	}
	payload := []byte(`{"model":"test","messages":[{"role":"user","content":"postgres capture"}]}`)
	snapshotJSON, _ := json.Marshal(run.RuntimeSnapshot)
	expiresAt := time.Now().UTC().Add(time.Hour)
	requestRecord := domain.ModelRequestRecord{
		Envelope: domain.ModelRequestEnvelope{
			ID: "modelreq_" + run.ID, RunID: run.ID, ConversationID: conversation.ID,
			ModelCallID: "call-postgres", Operation: "chat.completion", Provider: "test", Model: "test",
			RuntimeSnapshotHash: hashModelRequestBytes(snapshotJSON), PayloadHash: hashModelRequestBytes(payload),
			PayloadBytes: len(payload), Parameters: map[string]any{"model": "test"},
			SourceTokenBreakdown: map[string]int{}, MessageCount: 1, CreatedAt: time.Now().UTC(),
		},
		Capture: domain.ModelRequestCapture{
			Mode: domain.ModelRequestCaptureFull, Content: string(payload), ContentHash: hashModelRequestBytes(payload),
			OriginalBytes: len(payload), StoredBytes: len(payload), Reconstructable: true, ExpiresAt: &expiresAt,
		},
	}
	invalidRequest := requestRecord
	invalidRequest.Envelope.ID = ""
	if _, err := store.CreateModelRequestRecord(invalidRequest); err == nil {
		t.Fatal("expected invalid postgres model request error")
	}
	createdRequest, err := store.CreateModelRequestRecord(requestRecord)
	if err != nil || createdRequest.Envelope.Attempt != 1 {
		t.Fatalf("create model request record: record=%#v err=%v", createdRequest, err)
	}
	requestRecord.Envelope.ID += "_retry"
	createdRetry, err := store.CreateModelRequestRecord(requestRecord)
	if err != nil || createdRetry.Envelope.Attempt != 2 {
		t.Fatalf("create model request retry: record=%#v err=%v", createdRetry, err)
	}
	expiredRequest := requestRecord
	expiredRequest.Envelope.ID += "_expired"
	expiredRequest.Envelope.ModelCallID = "call-postgres-expired"
	expiredAt := time.Now().UTC().Add(-time.Minute)
	expiredRequest.Capture.ExpiresAt = &expiredAt
	if _, err := store.CreateModelRequestRecord(expiredRequest); err != nil {
		t.Fatalf("create expired model request: %v", err)
	}
	requestRecords, err := store.ListModelRequestRecords(run.ID)
	if err != nil || len(requestRecords) != 3 || requestRecords[0].Capture.Content != string(payload) {
		t.Fatalf("postgres model request round trip: records=%#v err=%v", requestRecords, err)
	}
	expiredPurged := false
	for _, item := range requestRecords {
		if item.Envelope.ID == expiredRequest.Envelope.ID {
			expiredPurged = item.Capture.Expired && item.Capture.Content == "" && item.Capture.StoredBytes == 0
		}
	}
	if !expiredPurged {
		t.Fatalf("expired postgres capture was not purged: %#v", requestRecords)
	}

	replay, ok, err := store.GetRunReplay(run.ID)
	if err != nil {
		t.Fatalf("run replay: %v", err)
	}
	if !ok {
		t.Fatal("expected replay")
	}
	if replay.RuntimeSnapshot == nil || replay.RuntimeSnapshot.SchemaVersion != domain.CurrentRuntimeSnapshotVersion {
		t.Fatalf("expected postgres snapshot round trip in replay, got %#v", replay.RuntimeSnapshot)
	}
	if replay.Summary.TotalTokens != 15 || !replay.Summary.TokenUsageEstimated {
		t.Fatalf("unexpected summary: %#v", replay.Summary)
	}
	if replay.RuntimeSnapshot.ContextAssembly.AssemblerVersion != "context-assembler-v1" {
		t.Fatalf("expected context assembly config round trip, got %#v", replay.RuntimeSnapshot.ContextAssembly)
	}
	if len(replay.Messages) != 2 || len(replay.Messages[1].Citations) != 1 || replay.Messages[1].Citations[0].SourceID != "S1" || len(replay.Steps) != 1 || len(replay.RunEvents) != 4 {
		t.Fatalf("unexpected replay counts: messages=%d steps=%d events=%d", len(replay.Messages), len(replay.Steps), len(replay.RunEvents))
	}
	conversationEvents, err := store.ListConversationRunEvents(conversation.ID)
	if err != nil {
		t.Fatalf("list conversation events: %v", err)
	}
	foundModelEvent := false
	for _, event := range conversationEvents {
		if event.ID == modelEvent.ID && event.RunID == run.ID && event.Type == domain.EventModelCompleted &&
			event.ConversationID == conversation.ID && event.StageID == step.ID &&
			event.TurnID == "turn-postgres-trace" && event.ParentEventID == "event-parent" {
			foundModelEvent = true
		}
	}
	if !foundModelEvent {
		t.Fatalf("conversation history did not include run-owned event: %#v", conversationEvents)
	}
}

func TestPostgresContextCompactionFailurePaths(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	postgresStore, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	conversation, _ := postgresStore.CreateConversation("compaction failures")
	run, _ := postgresStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	compaction := domain.ContextCompaction{
		ID: "cmp-failure-" + run.ID, ConversationID: conversation.ID, RunID: run.ID,
		Trigger: "hard", Generation: 1, Summary: "summary", SourceMessageIDs: []string{"m1"},
		SourceHash: "failure-hash-" + run.ID, BeforeTokens: 10, AfterTokens: 2,
		SummaryModel: "test", AlgorithmVersion: "v2",
	}
	validTerminal := domain.RunEvent{
		Type: domain.EventCompactionCompleted, RunID: run.ID, ConversationID: conversation.ID,
		Payload: map[string]any{"compaction_id": compaction.ID, "trigger": "hard", "status": "completed", "algorithm_version": "v2"},
	}
	if _, _, err := postgresStore.CommitContextCompaction(compaction, domain.RunEvent{
		Type: domain.EventCompactionFailed, RunID: run.ID, ConversationID: conversation.ID,
	}); err == nil {
		t.Fatal("mismatched terminal event should fail")
	}
	invalidPayload := validTerminal
	invalidPayload.Payload = map[string]any{"unsupported": make(chan int)}
	if _, _, err := postgresStore.CommitContextCompaction(compaction, invalidPayload); err == nil {
		t.Fatal("unencodable terminal payload should fail")
	}
	if _, ok, err := postgresStore.GetLatestContextCompaction("missing-conversation"); err != nil || ok {
		t.Fatalf("missing compaction lookup: ok=%v err=%v", ok, err)
	}
	created, _, err := postgresStore.CommitContextCompaction(compaction, validTerminal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := postgresStore.db.Exec(`UPDATE context_compactions SET source_event_ids = '{}'::jsonb WHERE id = $1`, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := postgresStore.GetLatestContextCompaction(conversation.ID); err == nil {
		t.Fatal("invalid source event ids should fail scanning")
	}
	if _, err := postgresStore.db.Exec(`UPDATE context_compactions SET source_event_ids = '[]'::jsonb, source_message_ids = '{}'::jsonb WHERE id = $1`, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := postgresStore.GetLatestContextCompaction(conversation.ID); err == nil {
		t.Fatal("invalid source message ids should fail scanning")
	}
	if _, err := postgresStore.db.Exec(`UPDATE context_compactions SET source_message_ids = '["m1"]'::jsonb WHERE id = $1`, created.ID); err != nil {
		t.Fatal(err)
	}
	if err := postgresStore.Close(); err != nil {
		t.Fatal(err)
	}
	closedCompaction := compaction
	closedCompaction.Generation = 0
	if _, _, err := postgresStore.CommitContextCompaction(closedCompaction, validTerminal); err == nil {
		t.Fatal("generation query on closed database should fail")
	}
	closedCompaction.Generation = 1
	if _, _, err := postgresStore.CommitContextCompaction(closedCompaction, validTerminal); err == nil {
		t.Fatal("transaction begin on closed database should fail")
	}
	if _, _, err := postgresStore.GetLatestContextCompaction(conversation.ID); err == nil {
		t.Fatal("lookup on closed database should fail")
	}
}

func TestPostgresListConversationRunEventsReturnsQueryErrorAfterClose(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatalf("new postgres store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close postgres store: %v", err)
	}
	if _, err := store.ListConversationRunEvents("conversation"); err == nil {
		t.Fatal("expected query against closed store to fail")
	}
}

func TestPostgresStoreLexicalRecall(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	store, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatalf("new postgres store: %v", err)
	}
	defer store.Close()

	workspaceID := "test-lexical-" + time.Now().UTC().Format("20060102150405.000000000")
	content := "AUTH-7F31 means the refresh token has expired."
	embedding := make([]float64, 1536)
	embedding[0] = 1
	document, err := store.CreateDocument(domain.Document{
		WorkspaceID: workspaceID, Title: "Authentication errors", Version: "auth-v1", ContentHash: "document-hash", SourceType: "text", Content: content,
	}, []domain.DocumentChunk{{ChunkSource: domain.ChunkSource{ParentID: "parent-auth", SectionPath: []string{"Errors"}, StartOffset: 0, EndOffset: len(content), DocumentVersion: "auth-v1", ContentHash: "chunk-hash"}, Content: content, TokenCount: 10}}, []domain.DocumentChunkEmbedding{{
		Provider: "test", Model: "embedding-v1", Dimensions: 1536, Embedding: embedding,
	}})
	if err != nil {
		t.Fatalf("create document: %v", err)
	}
	t.Cleanup(func() { _ = store.DeleteDocument(document.ID) })

	items, err := store.SearchDocumentChunksLexical(domain.DocumentSearch{
		Query: "AUTH-7F31 怎么解决", LexicalTerms: []string{"auth", "7f31", "怎么", "解决"},
		WorkspaceID: workspaceID, Limit: 5,
	})
	if err != nil {
		t.Fatalf("search document chunks lexically: %v", err)
	}
	if len(items) != 1 || items[0].Document.ID != document.ID || items[0].LexicalScore <= 0 {
		t.Fatalf("expected postgres lexical match, got %#v", items)
	}
	if items[0].Document.Version != "auth-v1" || items[0].Chunk.ParentID != "parent-auth" || items[0].Chunk.ContentHash != "chunk-hash" || strings.Join(items[0].Chunk.SectionPath, " > ") != "Errors" {
		t.Fatalf("expected postgres source details round trip, got %#v", items[0])
	}
}
