package store

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/inference/routing"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
	"encoding/json"
	"testing"
	"time"
)

func TestPostgresReplayKeepsHistoricalRuntimeSnapshotReadable(t *testing.T) {
	databaseURL := pgfixture.DatabaseURL(t)
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
	databaseURL := pgfixture.DatabaseURL(t)

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
		WorkspaceID: conversation.WorkspaceID,
		ID:          "memcand_" + message.ID, ConversationID: conversation.ID, RunID: run.ID,
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
	if replay.RuntimeSnapshot.ModelRouting.PolicyRevision != routing.PolicyRevision || len(replay.RuntimeSnapshot.ModelRouting.Routes) != 1 || replay.RuntimeSnapshot.ModelRouting.Routes[0].ID != "primary" {
		t.Fatalf("expected model route catalog round trip, got %#v", replay.RuntimeSnapshot.ModelRouting)
	}
	if replay.RuntimeSnapshot.ToolSecurityPolicy.Version != "round-trip-policy-v1" || replay.RuntimeSnapshot.ToolSecurityPolicy.DefaultAction != "deny" {
		t.Fatalf("expected Tool security policy round trip, got %#v", replay.RuntimeSnapshot.ToolSecurityPolicy)
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

func TestPostgresListConversationRunEventsReturnsQueryErrorAfterClose(t *testing.T) {
	databaseURL := pgfixture.DatabaseURL(t)
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
