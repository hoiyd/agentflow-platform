package store

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
	"testing"
)

func TestPostgresContextCompactionFailurePaths(t *testing.T) {
	databaseURL := pgfixture.DatabaseURL(t)
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
