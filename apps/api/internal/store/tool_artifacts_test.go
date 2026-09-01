package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func TestFileToolArtifactRoundTripReadSearchReplayAndExpiry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agentflow.json")
	fileStore, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	conversation, _ := fileStore.CreateConversation("artifact test")
	run, err := fileStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte(`{"rows":"` + strings.Repeat("recoverable-data-", 7000) + `needle"}`)
	now := time.Now().UTC()
	expires := now.Add(time.Hour)
	artifact := domain.ToolArtifact{
		ID: "tool_artifact_roundtrip", SchemaVersion: domain.CurrentToolArtifactSchemaVersion,
		RunID: run.ID, StageID: "stage-1", TurnID: "turn-1", ToolCallID: "call-1", ToolName: "future_tool",
		MediaType: "application/json", ContentHash: toolArtifactContentHash(content),
		OriginalByteSize: len(content), StoredByteSize: len(content), CreatedAt: now, ExpiresAt: &expires,
	}
	if _, err := fileStore.CreateToolArtifact(artifact, content); err != nil {
		t.Fatalf("create artifact: %v", err)
	}
	info, err := os.Stat(path + ".tool-artifacts")
	if err != nil {
		t.Fatalf("stat artifact directory: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("artifact directory permissions = %v", info.Mode().Perm())
	}
	contentInfo, err := os.Stat(filepath.Join(path+".tool-artifacts", artifact.ID+".bin"))
	if err != nil {
		t.Fatalf("stat artifact content: %v", err)
	}
	if contentInfo.Mode().Perm() != 0o600 {
		t.Fatalf("artifact content permissions = %v", contentInfo.Mode().Perm())
	}

	var recovered strings.Builder
	for offset := 0; ; {
		part, err := fileStore.ReadToolArtifact(run.ID, artifact.ID, offset, 4096)
		if err != nil {
			t.Fatalf("read artifact: %v", err)
		}
		recovered.WriteString(part.Content)
		if part.Complete {
			break
		}
		offset = part.NextOffset
	}
	if recovered.String() != string(content) {
		t.Fatal("bounded reads did not reconstruct exact artifact content")
	}
	search, err := fileStore.SearchToolArtifact(run.ID, artifact.ID, "needle", 5)
	if err != nil || len(search.Matches) != 1 || search.ScannedBytes != len(content) {
		t.Fatalf("search artifact: result=%#v err=%v", search, err)
	}
	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	replay, ok, err := reopened.GetRunReplay(run.ID)
	if err != nil || !ok || len(replay.ToolArtifacts) != 1 || replay.ToolArtifacts[0].ID != artifact.ID {
		t.Fatalf("replay lost artifact metadata: replay=%#v ok=%v err=%v", replay.ToolArtifacts, ok, err)
	}

	expired := artifact
	expired.ID = "tool_artifact_expired"
	expired.ToolCallID = "call-2"
	past := now.Add(-time.Minute)
	expired.ExpiresAt = &past
	if _, err := reopened.CreateToolArtifact(expired, content); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.ReadToolArtifact(run.ID, expired.ID, 0, 10); !errors.Is(err, ErrToolArtifactExpired) {
		t.Fatalf("expired artifact read error = %v", err)
	}
	cleaned, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(path+".tool-artifacts", expired.ID+".bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired artifact content was not purged: %v", err)
	}
	items, err := cleaned.ListToolArtifacts(run.ID)
	if err != nil || len(items) != 2 || !items[1].Expired {
		t.Fatalf("cleanup must preserve expired metadata: items=%#v err=%v", items, err)
	}
}

func TestToolArtifactMigrationAndSchemaRequirements(t *testing.T) {
	joined := strings.Join(postgresMigrations, "\n")
	for _, required := range []string{"CREATE TABLE IF NOT EXISTS tool_artifacts", "content bytea NOT NULL", "tool_artifacts_run_created_idx", "tool_artifacts_call_idx"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("postgres migration missing %q", required)
		}
	}
	foundContent := false
	for _, requirement := range postgresRequiredColumns {
		if requirement.Table == "tool_artifacts" && requirement.Column == "content" && requirement.UDTName == "bytea" && requirement.NotNull {
			foundContent = true
		}
	}
	if !foundContent {
		t.Fatal("postgres schema validation does not cover tool artifact content")
	}
}
