package store

import (
	"os"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

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
	run, err := store.CreateRun("agent_planner", conversation.ID, testRuntimeSnapshot())
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	contract := &domain.CompletionContract{
		ID: "contract_" + run.ID, Version: 1, Hash: "sha256:contract", SubjectType: "run_output",
		Verifiers: []domain.VerifierSpec{{ID: "schema", Type: domain.VerifierJSONSchema, Version: "1", Required: true,
			JSONSchema: &domain.JSONSchemaVerifierConfig{Schema: map[string]any{"type": "object"}}}},
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
			ContractVersion: 1, VerifierID: "schema", VerifierType: domain.VerifierJSONSchema,
			VerifierVersion: "1", Attempt: 1, SubjectHash: "sha256:subject", SnapshotHash: "sha256:snapshot",
			Status: domain.VerificationPassed, StartedAt: now, CompletedAt: now, Summary: "matched",
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
	if err != nil || !ok || verificationReplay.Run.CompletionContract == nil || len(verificationReplay.VerificationEvidence) != 1 || len(verificationReplay.VerificationArtifacts) != 1 {
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
		RunID: run.ID, StageID: step.ID, Type: domain.EventContextAssembled,
		Payload: map[string]any{"manifest": map[string]any{"id": "ctx-1", "assembler_version": "context-assembler-v1", "entries": []any{}}},
	}); err != nil {
		t.Fatalf("create context manifest trace: %v", err)
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
	if len(replay.Messages) != 1 || len(replay.Steps) != 1 || len(replay.RunEvents) != 2 {
		t.Fatalf("unexpected replay counts: messages=%d steps=%d events=%d", len(replay.Messages), len(replay.Steps), len(replay.RunEvents))
	}
}
