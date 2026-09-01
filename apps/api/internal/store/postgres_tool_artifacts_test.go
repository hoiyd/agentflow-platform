package store

import (
	"errors"
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
	if existing, err := postgresStore.CreateToolArtifact(artifact, content); err != nil || existing.ID != artifact.ID {
		t.Fatalf("idempotent postgres create: artifact=%#v err=%v", existing, err)
	}
	conflictingContent := []byte(`{"result":"different"}`)
	conflict := artifact
	conflict.ContentHash = toolArtifactContentHash(conflictingContent)
	conflict.OriginalByteSize = len(conflictingContent)
	conflict.StoredByteSize = len(conflictingContent)
	if _, err := postgresStore.CreateToolArtifact(conflict, conflictingContent); err == nil {
		t.Fatal("postgres idempotency conflict was accepted")
	}
	read, err := postgresStore.ReadToolArtifact(run.ID, artifact.ID, 0, 8)
	if err != nil || read.NextOffset != 8 || read.Complete {
		t.Fatalf("bounded postgres read: %#v err=%v", read, err)
	}
	search, err := postgresStore.SearchToolArtifact(run.ID, artifact.ID, "needle", 5)
	if err != nil || len(search.Matches) != 1 {
		t.Fatalf("postgres search: %#v err=%v", search, err)
	}
	complete, err := postgresStore.ReadToolArtifact(run.ID, artifact.ID, 0, len(content))
	if err != nil || !complete.Complete || complete.Content != string(content) {
		t.Fatalf("complete postgres read: %#v err=%v", complete, err)
	}
	if _, err := postgresStore.ReadToolArtifact(run.ID, artifact.ID, -1, 1); err == nil {
		t.Fatal("postgres accepted negative artifact offset")
	}
	if _, err := postgresStore.ReadToolArtifact(run.ID, artifact.ID, len(content)+1, 1); !errors.Is(err, ErrToolArtifactRange) {
		t.Fatalf("postgres range error = %v", err)
	}
	if _, err := postgresStore.ReadToolArtifact(run.ID, "missing", 0, 1); !IsNotFound(err) {
		t.Fatalf("postgres missing read error = %v", err)
	}
	if _, err := postgresStore.SearchToolArtifact(run.ID, artifact.ID, "", 1); err == nil {
		t.Fatal("postgres accepted empty artifact search")
	}
	if _, err := postgresStore.SearchToolArtifact(run.ID, "missing", "needle", 1); !IsNotFound(err) {
		t.Fatalf("postgres missing search error = %v", err)
	}
	if _, err := postgresStore.ListToolArtifacts("missing"); !IsNotFound(err) {
		t.Fatalf("postgres missing run list error = %v", err)
	}

	past := time.Now().UTC().Add(-time.Minute)
	expired := artifact
	expired.ID += "_expired"
	expired.ToolCallID = "call-expired"
	expired.ExpiresAt = &past
	if _, err := postgresStore.CreateToolArtifact(expired, content); err != nil {
		t.Fatal(err)
	}
	if _, err := postgresStore.ReadToolArtifact(run.ID, expired.ID, 0, 1); !errors.Is(err, ErrToolArtifactExpired) {
		t.Fatalf("postgres expired read error = %v", err)
	}
	if _, err := postgresStore.SearchToolArtifact(run.ID, expired.ID, "needle", 1); !errors.Is(err, ErrToolArtifactExpired) {
		t.Fatalf("postgres expired search error = %v", err)
	}
	items, err := postgresStore.ListToolArtifacts(run.ID)
	if err != nil || len(items) != 2 || !items[0].Expired && !items[1].Expired {
		t.Fatalf("postgres expired metadata: items=%#v err=%v", items, err)
	}
	if err := postgresStore.purgeExpiredToolArtifactContent(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, expiredContent, ok, err := postgresStore.getToolArtifact(run.ID, expired.ID)
	if err != nil || !ok || len(expiredContent) != 0 {
		t.Fatalf("postgres expired content purge: ok=%v bytes=%d err=%v", ok, len(expiredContent), err)
	}
	replay, ok, err := postgresStore.GetRunReplay(run.ID)
	if err != nil || !ok || len(replay.ToolArtifacts) != 2 {
		t.Fatalf("postgres replay artifact metadata: %#v ok=%v err=%v", replay.ToolArtifacts, ok, err)
	}
}

func TestPostgresToolArtifactScannerErrors(t *testing.T) {
	want := errors.New("scan failed")
	row := scannerFunc(func(...any) error { return want })
	if _, err := scanToolArtifact(row); !errors.Is(err, want) {
		t.Fatalf("metadata scan error = %v", err)
	}
	if _, _, err := scanToolArtifactWithContent(row); !errors.Is(err, want) {
		t.Fatalf("content scan error = %v", err)
	}
}

type scannerFunc func(...any) error

func (f scannerFunc) Scan(values ...any) error { return f(values...) }
