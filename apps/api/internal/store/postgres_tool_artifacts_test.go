package store

import (
	"os"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func TestPostgresToolArtifactRoundTrip(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	postgresStore, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer postgresStore.Close()
	conversation, err := postgresStore.CreateConversation("postgres artifact")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = postgresStore.DeleteConversation(conversation.ID) })
	run, err := postgresStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte(`{"result":"postgres needle"}`)
	expires := time.Now().UTC().Add(time.Hour)
	artifact := domain.ToolArtifact{
		ID: "tool_artifact_postgres_" + run.ID, SchemaVersion: domain.CurrentToolArtifactSchemaVersion,
		RunID: run.ID, ToolCallID: "call-1", ToolName: "future_tool", MediaType: "application/json",
		ContentHash: toolArtifactContentHash(content), OriginalByteSize: len(content), StoredByteSize: len(content),
		CreatedAt: time.Now().UTC(), ExpiresAt: &expires,
	}
	if _, err := postgresStore.CreateToolArtifact(artifact, content); err != nil {
		t.Fatal(err)
	}
	read, err := postgresStore.ReadToolArtifact(run.ID, artifact.ID, 0, 8)
	if err != nil || read.NextOffset != 8 || read.Complete {
		t.Fatalf("bounded postgres read: %#v err=%v", read, err)
	}
	search, err := postgresStore.SearchToolArtifact(run.ID, artifact.ID, "needle", 5)
	if err != nil || len(search.Matches) != 1 {
		t.Fatalf("postgres search: %#v err=%v", search, err)
	}
	replay, ok, err := postgresStore.GetRunReplay(run.ID)
	if err != nil || !ok || len(replay.ToolArtifacts) != 1 {
		t.Fatalf("postgres replay artifact metadata: %#v ok=%v err=%v", replay.ToolArtifacts, ok, err)
	}
}
