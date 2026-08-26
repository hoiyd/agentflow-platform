package store

import (
	"encoding/json"
	"os"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestFileAndPostgresProduceByteEquivalentProjection(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	postgresStore, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer postgresStore.Close()
	fileStore, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatal(err)
	}

	fileConversation, err := fileStore.CreateConversation("projection parity")
	if err != nil {
		t.Fatal(err)
	}
	fileRun, err := fileStore.CreateRun("agent_planner", fileConversation.ID, testRuntimeSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	insertPostgresProjectionIdentity(t, postgresStore, fileConversation, fileRun)
	t.Cleanup(func() { _ = postgresStore.DeleteConversation(fileConversation.ID) })

	appendProjectionFixture(t, fileStore, fileRun)
	appendProjectionFixture(t, postgresStore, fileRun)
	fileReplay, ok, err := fileStore.GetRunReplay(fileRun.ID)
	if err != nil || !ok {
		t.Fatalf("file replay: ok=%t err=%v", ok, err)
	}
	postgresReplay, ok, err := postgresStore.GetRunReplay(fileRun.ID)
	if err != nil || !ok {
		t.Fatalf("postgres replay: ok=%t err=%v", ok, err)
	}

	fileJSON, err := json.Marshal(fileReplay.Projection)
	if err != nil {
		t.Fatal(err)
	}
	postgresJSON, err := json.Marshal(postgresReplay.Projection)
	if err != nil {
		t.Fatal(err)
	}
	if string(fileJSON) != string(postgresJSON) {
		t.Fatalf("projection adapters diverged:\nfile=%s\npostgres=%s", fileJSON, postgresJSON)
	}
}

func insertPostgresProjectionIdentity(t *testing.T, target *PostgresStore, conversation domain.Conversation, run domain.Run) {
	t.Helper()
	snapshot, err := json.Marshal(run.RuntimeSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := json.Marshal(run.CompletionContract)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.db.Exec(`
		INSERT INTO conversations (id, workspace_id, title, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)`,
		conversation.ID, conversation.WorkspaceID, conversation.Title, conversation.CreatedAt, conversation.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := target.db.Exec(`
		INSERT INTO runs (id, workspace_id, agent_id, conversation_id, status, error,
			runtime_snapshot, completion_contract, verification_status, started_at,
			execution_started_at, active_runtime_ms, heartbeat_at, completed_at, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		run.ID, run.WorkspaceID, run.AgentID, run.ConversationID, string(run.Status), run.Error,
		snapshot, contract, string(run.VerificationStatus), run.StartedAt, run.ExecutionStartedAt,
		run.ActiveRuntimeMS, run.HeartbeatAt, run.CompletedAt, run.CreatedAt, run.UpdatedAt); err != nil {
		t.Fatal(err)
	}
}

type projectionEventStore interface {
	CreateRunEvent(domain.RunEvent) (domain.RunEvent, error)
}

func appendProjectionFixture(t *testing.T, target projectionEventStore, run domain.Run) {
	t.Helper()
	events := []domain.RunEvent{
		{Type: domain.EventRunCreated},
		{Type: domain.EventRunStarted},
		{Type: domain.EventStageStarted, StageID: "stage-1"},
		{Type: domain.EventTurnStarted, StageID: "stage-1", TurnID: "turn-1"},
		{Type: domain.EventModelStarted, StageID: "stage-1", TurnID: "turn-1", Payload: map[string]any{"model_call_id": "model-1"}},
		{Type: domain.EventModelCompleted, StageID: "stage-1", TurnID: "turn-1", Payload: map[string]any{"model_call_id": "model-1", "prompt_tokens": 8, "completion_tokens": 5, "total_tokens": 13}},
		{Type: domain.EventTurnCompleted, StageID: "stage-1", TurnID: "turn-1"},
		{Type: domain.EventStageCompleted, StageID: "stage-1"},
	}
	for _, item := range events {
		item.RunID = run.ID
		item.ConversationID = run.ConversationID
		if _, err := target.CreateRunEvent(item); err != nil {
			t.Fatalf("create %s: %v", item.Type, err)
		}
	}
}
