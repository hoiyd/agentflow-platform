package store

import (
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/domain"
)

func TestFileStoreRejectsLiveOnlyRunEvents(t *testing.T) {
	fileStore, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := fileStore.CreateConversation("live events")
	if err != nil {
		t.Fatal(err)
	}
	run, err := fileStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, eventType := range []domain.RunEventType{domain.EventModelDelta, domain.EventRunProgress} {
		_, err = fileStore.CreateRunEvent(domain.RunEvent{Type: eventType, RunID: run.ID, Payload: map[string]any{"delta": "hello"}})
		if err == nil || !strings.Contains(err.Error(), "live-only") {
			t.Fatalf("expected %s live-only persistence error, got %v", eventType, err)
		}
	}
	items, listErr := fileStore.ListRunEvents(run.ID)
	if listErr != nil || len(items) != 0 {
		t.Fatalf("live event reached replay: items=%#v err=%v", items, listErr)
	}
}

func TestFileStoreUsageLedgerIsIdempotentAndPersistsSettlementOverage(t *testing.T) {
	path := t.TempDir() + "/agentflow.json"
	store, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := store.CreateConversation("usage ledger")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	snapshot := testRuntimeSnapshot()
	snapshot.RunBudget = &domain.RuntimeRunBudget{MaxModelCalls: 1, MaxTotalTokens: 10}
	run, err := store.CreateRunWithContract("agent_planner", conversation.ID, snapshot, nil)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, applied, applyErr := store.ApplyRunUsage(domain.RunUsageEntry{
		ID: "usage-invalid", RunID: run.ID, OperationID: "call-invalid",
		Kind: domain.UsageModelReservation, Purpose: domain.UsagePurposePrimary,
		ModelCalls: 1, PromptTokens: -5, CompletionTokens: 1, TotalTokens: -4,
		Timestamp: time.Now().UTC(),
	}); applyErr == nil || applied {
		t.Fatalf("invalid negative usage was accepted: applied=%t err=%v", applied, applyErr)
	}
	now := time.Now().UTC()
	reservation := domain.RunUsageEntry{
		ID: "usage-reserve-1", RunID: run.ID, OperationID: "model-call-1",
		Kind: domain.UsageModelReservation, Purpose: domain.UsagePurposePrimary,
		Model: "test", ModelCalls: 1, PromptTokens: 5, CompletionTokens: 1,
		TotalTokens: 6, Estimated: true, Timestamp: now,
	}
	ledger, applied, err := store.ApplyRunUsage(reservation)
	if err != nil || !applied || ledger.Totals.OpenReservations != 1 {
		t.Fatalf("reserve usage: applied=%t ledger=%#v err=%v", applied, ledger, err)
	}
	duplicate := reservation
	duplicate.ID = "usage-reserve-retry"
	duplicate.Timestamp = now.Add(time.Second)
	ledger, applied, err = store.ApplyRunUsage(duplicate)
	if err != nil || applied || len(ledger.Entries) != 1 {
		t.Fatalf("duplicate reservation was not idempotent: applied=%t ledger=%#v err=%v", applied, ledger, err)
	}
	settlement := domain.RunUsageEntry{
		ID: "usage-settle-1", RunID: run.ID, OperationID: "model-call-1",
		Kind: domain.UsageModelSettlement, Purpose: domain.UsagePurposePrimary,
		Model: "test", ModelCalls: 1, PromptTokens: 7, CompletionTokens: 5,
		TotalTokens: 12, Timestamp: now.Add(2 * time.Second),
	}
	ledger, applied, err = store.ApplyRunUsage(settlement)
	exceeded, ok := budget.AsExceeded(err)
	if !applied || !ok || exceeded.Resource != budget.ResourceTotalTokens || ledger.Totals.TotalTokens != 12 || ledger.Totals.OpenReservations != 0 {
		t.Fatalf("settlement overage was not persisted: applied=%t ledger=%#v err=%v", applied, ledger, err)
	}

	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	replay, ok, err := reopened.GetRunReplay(run.ID)
	if err != nil || !ok || replay.UsageLedger.Totals.TotalTokens != 12 || len(replay.UsageLedger.Entries) != 2 {
		t.Fatalf("usage replay round trip: ok=%t ledger=%#v err=%v", ok, replay.UsageLedger, err)
	}
}

func TestFileStoreActiveRuntimeExcludesWaitingForUser(t *testing.T) {
	store, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := store.CreateConversation("runtime segments")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := store.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatalf("create run: %v", err)
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
		t.Fatalf("waiting time leaked into active runtime: before=%d resumed=%#v err=%v", activeBeforeWait, resumed, err)
	}
}

func testRuntimeSnapshot() domain.RuntimeSnapshot {
	return domain.RuntimeSnapshot{
		SchemaVersion:   domain.CurrentRuntimeSnapshotVersion,
		RunBudget:       &domain.RuntimeRunBudget{},
		Mode:            "single",
		Agent:           domain.RuntimeAgentSnapshot{ID: "agent_planner", Executor: domain.DefaultAgentExecutor},
		Model:           domain.RuntimeModelSnapshot{Provider: "local", Model: "test"},
		ContextAssembly: domain.ContextAssemblyConfig{AssemblerVersion: "context-assembler-v1", ContextWindowTokens: 128000, OutputReserveTokens: 8192, SafetyMarginTokens: 4096, HistoryMaxTokens: 64000, MemoryMaxTokens: 8000, KnowledgeMaxTokens: 16000},
	}
}

func TestFileStoreRuntimeSnapshotRoundTripAndReplay(t *testing.T) {
	path := t.TempDir() + "/agentflow.json"
	first, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := first.CreateConversation("snapshot round trip")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	snapshot := testRuntimeSnapshot()
	snapshot.Agent.SystemPrompt = "frozen prompt"
	run, err := first.CreateRunWithContract("agent_planner", conversation.ID, snapshot, nil)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := first.CreateRunEvent(domain.RunEvent{RunID: run.ID, Type: domain.EventContextAssembled, Payload: map[string]any{
		"manifest": map[string]any{"id": "ctx-1", "assembler_version": "context-assembler-v1", "entries": []any{}},
	}}); err != nil {
		t.Fatalf("create context manifest event: %v", err)
	}

	second, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	loaded, ok, err := second.GetRun(run.ID)
	if err != nil || !ok {
		t.Fatalf("get run after reopen: ok=%v err=%v", ok, err)
	}
	if loaded.RuntimeSnapshot == nil || loaded.RuntimeSnapshot.Agent.SystemPrompt != "frozen prompt" {
		t.Fatalf("unexpected loaded snapshot: %#v", loaded.RuntimeSnapshot)
	}
	replay, ok, err := second.GetRunReplay(run.ID)
	if err != nil || !ok {
		t.Fatalf("get replay: ok=%v err=%v", ok, err)
	}
	if replay.RuntimeSnapshot == nil || replay.RuntimeSnapshot.Agent.SystemPrompt != "frozen prompt" {
		t.Fatalf("replay did not return snapshot: %#v", replay.RuntimeSnapshot)
	}
	if replay.RuntimeSnapshot.ContextAssembly.AssemblerVersion != "context-assembler-v1" || len(replay.RunEvents) != 1 || replay.RunEvents[0].Type != domain.EventContextAssembled {
		t.Fatalf("context assembly config or manifest event did not round trip: snapshot=%#v events=%#v", replay.RuntimeSnapshot, replay.RunEvents)
	}
}

func TestFileStoreReplayKeepsHistoricalRuntimeSnapshotReadable(t *testing.T) {
	fileStore, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("historical snapshot replay")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := fileStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	fileStore.mu.Lock()
	for index := range fileStore.data.Runs {
		if fileStore.data.Runs[index].ID == run.ID {
			fileStore.data.Runs[index].RuntimeSnapshot.SchemaVersion = domain.LegacyRuntimeSnapshotVersion
			break
		}
	}
	err = fileStore.saveLocked()
	fileStore.mu.Unlock()
	if err != nil {
		t.Fatalf("persist historical fixture: %v", err)
	}

	reopened, err := NewFileStore(fileStore.path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	replay, ok, err := reopened.GetRunReplay(run.ID)
	if err != nil || !ok {
		t.Fatalf("get historical replay: ok=%v err=%v", ok, err)
	}
	if replay.RuntimeSnapshot == nil || replay.RuntimeSnapshot.SchemaVersion != domain.LegacyRuntimeSnapshotVersion {
		t.Fatalf("historical snapshot was not preserved: %#v", replay.RuntimeSnapshot)
	}
}

func TestFileStoreVerificationContractEvidenceAndArtifactsRoundTrip(t *testing.T) {
	path := t.TempDir() + "/agentflow.json"
	first, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := first.CreateConversation("verification round trip")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	contract := &domain.CompletionContract{
		ID: "contract_test", Version: domain.CurrentCompletionContractVersion, Hash: "sha256:contract", SubjectType: "run_output",
		Verifiers: []domain.VerifierSpec{{
			ID: "schema", Type: domain.VerifierJSONSchema, Version: "1", Required: true,
			Config: map[string]any{"schema": map[string]any{"type": "object"}},
		}},
		Policy: domain.VerificationPolicy{Mode: domain.VerificationAllMustPass, MaxAttempts: 2, OnExhausted: domain.VerificationWaitForUser},
	}
	run, err := first.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), contract)
	if err != nil {
		t.Fatalf("create run with contract: %v", err)
	}
	if run.VerificationStatus != domain.VerificationPending {
		t.Fatalf("expected pending verification, got %q", run.VerificationStatus)
	}
	contract.Verifiers[0].Config["schema"].(map[string]any)["type"] = "string"

	now := time.Now().UTC()
	exitCode := 0
	record := domain.VerificationRecord{
		Evidence: domain.VerificationEvidence{
			ID: "evidence_test", RunID: run.ID, ContractID: "contract_test", ContractVersion: domain.CurrentCompletionContractVersion,
			VerifierID: "schema", VerifierType: domain.VerifierJSONSchema, VerifierVersion: "1",
			Attempt: 1, SubjectHash: "sha256:subject", SnapshotHash: "sha256:snapshot",
			Status: domain.VerificationPassed, StartedAt: now, CompletedAt: now,
			ExitCode: &exitCode, Summary: "schema matched", Details: map[string]any{"matched": true}, ArtifactIDs: []string{"artifact_test"},
		},
		Artifacts: []domain.VerificationArtifact{{
			ID: "artifact_test", RunID: run.ID, EvidenceID: "evidence_test", Kind: "verifier_output",
			MediaType: "text/plain", Content: "schema matched", ContentHash: "sha256:output",
			ByteSize: 14, CreatedAt: now,
		}},
	}
	if err := first.AppendVerificationRecord(record); err != nil {
		t.Fatalf("append verification record: %v", err)
	}
	if _, err := first.UpdateRunVerificationStatus(run.ID, domain.VerificationPassed); err != nil {
		t.Fatalf("update verification status: %v", err)
	}

	second, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	replay, ok, err := second.GetRunReplay(run.ID)
	if err != nil || !ok {
		t.Fatalf("get replay: ok=%v err=%v", ok, err)
	}
	if replay.Run.CompletionContract == nil || replay.Run.CompletionContract.Verifiers[0].Config["schema"].(map[string]any)["type"] != "object" {
		t.Fatalf("contract was not frozen: %#v", replay.Run.CompletionContract)
	}
	if replay.Run.VerificationStatus != domain.VerificationPassed || len(replay.VerificationEvidence) != 1 || len(replay.VerificationArtifacts) != 1 {
		t.Fatalf("verification did not round trip: run=%#v evidence=%#v artifacts=%#v", replay.Run, replay.VerificationEvidence, replay.VerificationArtifacts)
	}
	replay.VerificationEvidence[0].ArtifactIDs[0] = "mutated"
	replay.VerificationEvidence[0].Details["matched"] = false
	freshEvidence, err := second.ListVerificationEvidence(run.ID)
	if err != nil || freshEvidence[0].ArtifactIDs[0] != "artifact_test" || freshEvidence[0].Details["matched"] != true {
		t.Fatalf("stored evidence was mutable through a read result: %#v err=%v", freshEvidence, err)
	}
}

func TestFileStoreRunLifecycle(t *testing.T) {
	store, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := store.CreateConversation("Runtime test")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}

	run, err := store.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if run.Status != domain.RunQueued {
		t.Fatalf("expected queued run, got %s", run.Status)
	}

	run, err = store.UpdateRunStatus(run.ID, domain.RunRunning, "")
	if err != nil {
		t.Fatalf("mark running: %v", err)
	}
	if run.StartedAt == nil || run.Status != domain.RunRunning {
		t.Fatalf("expected running run with started_at, got %#v", run)
	}
	if run.HeartbeatAt == nil {
		t.Fatalf("expected running run with heartbeat_at, got %#v", run)
	}

	run, err = store.UpdateRunAgent(run.ID, "agent_coding")
	if err != nil {
		t.Fatalf("update run agent: %v", err)
	}
	if run.AgentID != "agent_coding" {
		t.Fatalf("expected updated run agent, got %q", run.AgentID)
	}

	run, err = store.UpdateRunStatus(run.ID, domain.RunCompleted, "")
	if err != nil {
		t.Fatalf("mark completed: %v", err)
	}
	if run.CompletedAt == nil || run.Status != domain.RunCompleted {
		t.Fatalf("expected completed run with completed_at, got %#v", run)
	}
}

func TestFileStoreRunEventsHaveStrictSequence(t *testing.T) {
	store, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := store.CreateConversation("Event sequence")
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, eventType := range []domain.RunEventType{domain.EventRunCreated, domain.EventRunStarted, domain.EventRunCompleted} {
		event, err := store.CreateRunEvent(domain.RunEvent{RunID: run.ID, ConversationID: conversation.ID, Type: eventType})
		if err != nil {
			t.Fatal(err)
		}
		if event.Sequence < 1 {
			t.Fatalf("invalid sequence: %d", event.Sequence)
		}
	}
	events, err := store.ListRunEvents(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("events = %d", len(events))
	}
	for index, event := range events {
		if event.Sequence != int64(index+1) {
			t.Fatalf("sequence[%d] = %d", index, event.Sequence)
		}
		if event.SchemaVersion != domain.CurrentRunEventSchemaVersion {
			t.Fatalf("schema version = %d", event.SchemaVersion)
		}
	}
}

func TestFileStoreListStaleRunningRuns(t *testing.T) {
	store, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := store.CreateConversation("Stale run test")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	staleRun, err := store.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatalf("create stale run: %v", err)
	}
	staleRun, err = store.UpdateRunStatus(staleRun.ID, domain.RunRunning, "")
	if err != nil {
		t.Fatalf("mark stale run running: %v", err)
	}
	freshRun, err := store.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatalf("create fresh run: %v", err)
	}
	if _, err := store.UpdateRunStatus(freshRun.ID, domain.RunRunning, ""); err != nil {
		t.Fatalf("mark fresh run running: %v", err)
	}
	completedRun, err := store.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatalf("create completed run: %v", err)
	}
	if _, err := store.UpdateRunStatus(completedRun.ID, domain.RunCompleted, ""); err != nil {
		t.Fatalf("mark completed run: %v", err)
	}

	past := time.Now().UTC().Add(-10 * time.Minute)
	store.mu.Lock()
	for i := range store.data.Runs {
		if store.data.Runs[i].ID == staleRun.ID {
			store.data.Runs[i].HeartbeatAt = &past
		}
	}
	store.mu.Unlock()

	runs, err := store.ListStaleRunningRuns(time.Now().UTC().Add(-time.Minute))
	if err != nil {
		t.Fatalf("list stale runs: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != staleRun.ID {
		t.Fatalf("expected only stale running run, got %#v", runs)
	}
}

func TestFileStoreDeleteConversationCascades(t *testing.T) {
	store, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	conversation, err := store.CreateConversation("Delete me")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if _, err := store.AddMessage(conversation.ID, "user", "hello"); err != nil {
		t.Fatalf("add message: %v", err)
	}
	run, err := store.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := store.CreateCollaborationStep(domain.CollaborationStep{
		RunID:          run.ID,
		ConversationID: conversation.ID,
		Role:           "planner",
		Status:         domain.CollaborationStepCompleted,
		Input:          "plan",
		Output:         "done",
	}); err != nil {
		t.Fatalf("create collaboration step: %v", err)
	}

	if err := store.DeleteConversation(conversation.ID); err != nil {
		t.Fatalf("delete conversation: %v", err)
	}

	if _, ok, err := store.GetConversation(conversation.ID); err != nil {
		t.Fatalf("get conversation after delete: %v", err)
	} else if ok {
		t.Fatal("expected conversation to be removed")
	}

	messages, err := store.ListMessages(conversation.ID)
	if err != nil {
		t.Fatalf("list messages after delete: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("expected no messages after delete, got %d", len(messages))
	}

	if _, ok, err := store.GetRun(run.ID); err != nil {
		t.Fatalf("get run after delete: %v", err)
	} else if ok {
		t.Fatal("expected run to be removed")
	}

	steps, err := store.ListCollaborationSteps(run.ID)
	if err != nil {
		t.Fatalf("list steps after delete: %v", err)
	}
	if len(steps) != 0 {
		t.Fatalf("expected no steps after delete, got %d", len(steps))
	}

	events, err := store.ListRunEvents(run.ID)
	if err != nil {
		t.Fatalf("list run events after delete: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no run events after delete, got %d", len(events))
	}
}

func TestFileStoreTraceReplay(t *testing.T) {
	store, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := store.CreateConversation("Trace test")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if _, err := store.AddMessage(conversation.ID, "user", "hello"); err != nil {
		t.Fatalf("add message: %v", err)
	}
	run, err := store.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	run, err = store.UpdateRunStatus(run.ID, domain.RunRunning, "")
	if err != nil {
		t.Fatalf("mark running: %v", err)
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
	if _, err := store.CreateRunEvent(domain.RunEvent{
		RunID:   run.ID,
		StageID: step.ID,
		Type:    domain.EventModelCompleted,
		Payload: map[string]any{
			"prompt_tokens":         10,
			"completion_tokens":     5,
			"total_tokens":          15,
			"token_usage_estimated": true,
		},
	}); err != nil {
		t.Fatalf("create llm trace: %v", err)
	}
	if _, err := store.CreateRunEvent(domain.RunEvent{
		RunID:   run.ID,
		StageID: step.ID,
		Type:    domain.EventToolCompleted,
		Payload: map[string]any{"tool_name": "calculator"},
	}); err != nil {
		t.Fatalf("create tool trace: %v", err)
	}
	if _, err := store.CreateRunEvent(domain.RunEvent{
		RunID:   run.ID,
		StageID: step.ID,
		Type:    domain.EventModelFailed,
		Payload: map[string]any{"error": "boom"},
	}); err != nil {
		t.Fatalf("create error trace: %v", err)
	}
	if _, err := store.CreateRunEvent(domain.RunEvent{
		RunID: run.ID, StageID: step.ID, Type: domain.EventToolFailed,
		Payload: map[string]any{"tool_name": "calculator", "error": "tool failed"},
	}); err != nil {
		t.Fatalf("create tool error trace: %v", err)
	}
	if _, err := store.CreateRunEvent(domain.RunEvent{
		RunID: run.ID, Type: domain.EventCompactionFailed,
		Payload: map[string]any{"error": "summary unavailable"},
	}); err != nil {
		t.Fatalf("create compaction error trace: %v", err)
	}

	replay, ok, err := store.GetRunReplay(run.ID)
	if err != nil || !ok {
		t.Fatalf("run replay: ok=%v err=%v", ok, err)
	}
	summary := replay.Summary
	if summary.TotalTokens != 15 || summary.PromptTokens != 10 || summary.CompletionTokens != 5 {
		t.Fatalf("unexpected token summary: %#v", summary)
	}
	if !summary.TokenUsageEstimated {
		t.Fatalf("expected estimated token summary: %#v", summary)
	}
	if summary.LLMCalls != 1 || summary.ToolCalls != 2 || summary.ErrorCount != 3 {
		t.Fatalf("unexpected call summary: %#v", summary)
	}

	if replay.Run.ID != run.ID || replay.Conversation.ID != conversation.ID {
		t.Fatalf("unexpected replay identity: %#v", replay)
	}
	if len(replay.Messages) != 1 || len(replay.Steps) != 1 || len(replay.RunEvents) != 5 {
		t.Fatalf("unexpected replay counts: messages=%d steps=%d events=%d", len(replay.Messages), len(replay.Steps), len(replay.RunEvents))
	}
}
