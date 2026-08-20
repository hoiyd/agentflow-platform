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
	run, err := postgresStore.CreateRun("agent_planner", conversation.ID, testRuntimeSnapshot())
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
	if _, err := postgresStore.CompleteToolEffect(effect.IdempotencyKey, []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	checkpoints, err := postgresStore.ListStageCheckpoints(run.ID)
	if err != nil || len(checkpoints) != 1 || checkpoints[0].Status != domain.CheckpointExecuting {
		t.Fatalf("checkpoint round trip: %#v err=%v", checkpoints, err)
	}
	effects, err := postgresStore.ListToolEffects(run.ID)
	if err != nil || len(effects) != 1 || effects[0].Status != domain.ToolEffectCommitted {
		t.Fatalf("effect round trip: %#v err=%v", effects, err)
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
	run, err := store.CreateRun("agent_planner", conversation.ID, snapshot)
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
	run, err := store.CreateRun("agent_planner", conversation.ID, testRuntimeSnapshot())
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
	run, err := store.CreateRun("agent_planner", conversation.ID, testRuntimeSnapshot())
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
	compaction, err := store.CreateContextCompaction(domain.ContextCompaction{
		ConversationID: conversation.ID, RunID: run.ID, Trigger: "soft", Summary: "summary",
		SourceMessageIDs: []string{"message-1"}, SourceHash: "source-hash", BeforeTokens: 100,
		AfterTokens: 20, SummaryModel: "test", AlgorithmVersion: "context-compaction-v1",
	})
	if err != nil {
		t.Fatalf("create context compaction: %v", err)
	}
	latest, ok, err := store.GetLatestContextCompaction(conversation.ID)
	if err != nil || !ok || latest.ID != compaction.ID {
		t.Fatalf("postgres compaction round trip: ok=%v err=%v item=%#v", ok, err, latest)
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
	if len(replay.Messages) != 2 || len(replay.Messages[1].Citations) != 1 || replay.Messages[1].Citations[0].SourceID != "S1" || len(replay.Steps) != 1 || len(replay.RunEvents) != 2 {
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
