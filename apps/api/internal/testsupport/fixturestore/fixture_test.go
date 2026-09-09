package fixturestore

import (
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func TestFixtureIsolationAndArtifactLifecycle(t *testing.T) {
	s := New()
	conversation, err := s.CreateConversationInWorkspace("team-a", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateRunWithContract("agent_planner", conversation.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("evidence value")
	artifact := domain.ToolArtifact{ID: "tool_artifact_fixture", RunID: run.ID, ToolCallID: "call", ToolName: "read", SchemaVersion: domain.CurrentToolArtifactSchemaVersion, MediaType: "text/plain", StoredByteSize: len(content), OriginalByteSize: len(content), ContentHash: store.ToolArtifactContentHash(content), CreatedAt: time.Now().UTC()}
	if _, err = s.CreateToolArtifact(artifact, content); err != nil {
		t.Fatal(err)
	}
	content[0] = 'X'
	read, err := s.ReadToolArtifact(run.ID, artifact.ID, 0, 4)
	if err != nil || read.Content != "evid" || read.Complete {
		t.Fatalf("bounded owned copy: %+v %v", read, err)
	}
	if _, err = s.ReadToolArtifact("wrong-run", artifact.ID, 0, 4); !store.IsNotFound(err) {
		t.Fatalf("run scope: %v", err)
	}
	if _, err = s.ReadToolArtifact(run.ID, artifact.ID, 100, 4); err != store.ErrToolArtifactRange {
		t.Fatalf("range: %v", err)
	}
	result, err := s.SearchToolArtifact(run.ID, artifact.ID, "value", 1)
	if err != nil || len(result.Matches) != 1 {
		t.Fatalf("search: %+v %v", result, err)
	}
	if _, err = New().ReadToolArtifact(run.ID, artifact.ID, 0, 4); !store.IsNotFound(err) {
		t.Fatalf("new fixture must not reopen state: %v", err)
	}
	if _, err = s.CreateToolArtifact(artifact, []byte("evidence value")); err != nil || len(s.data.ToolArtifacts) != 1 {
		t.Fatalf("idempotent create: %v", err)
	}
	conflict := artifact
	conflict.ToolCallID = "other-call"
	if _, err = s.CreateToolArtifact(conflict, []byte("evidence value")); err == nil {
		t.Fatal("conflicting identity accepted")
	}
	if _, err = New().CreateToolArtifact(artifact, []byte("evidence value")); !store.IsNotFound(err) {
		t.Fatalf("missing owner accepted: %v", err)
	}
	if _, err = s.ListToolArtifacts("missing"); !store.IsNotFound(err) {
		t.Fatalf("missing artifact list owner: %v", err)
	}
	if _, err = s.ReadToolArtifact(run.ID, artifact.ID, -1, 4); err == nil {
		t.Fatal("negative offset accepted")
	}
	if _, err = s.SearchToolArtifact(run.ID, artifact.ID, "", 1); err == nil {
		t.Fatal("empty search accepted")
	}
	expired := time.Now().Add(-time.Hour)
	s.data.ToolArtifacts[0].ExpiresAt = &expired
	if _, err = s.CreateToolArtifact(artifact, []byte("evidence value")); err != store.ErrToolArtifactExpired {
		t.Fatalf("expired idempotent create: %v", err)
	}
	if _, err = s.ReadToolArtifact(run.ID, artifact.ID, 0, 4); err != store.ErrToolArtifactExpired {
		t.Fatalf("expired read: %v", err)
	}
	if _, err = s.SearchToolArtifact(run.ID, artifact.ID, "value", 1); err != store.ErrToolArtifactExpired {
		t.Fatalf("expired search: %v", err)
	}
	if err = s.DeleteConversation(conversation.ID); err != nil {
		t.Fatal(err)
	}
	if len(s.artifactContent) != 0 {
		t.Fatal("conversation deletion retained artifact bytes")
	}
	if _, err = s.ReadToolArtifact(run.ID, artifact.ID, 0, 4); !store.IsNotFound(err) {
		t.Fatalf("deleted artifact: %v", err)
	}
}

func TestFixtureRejectsInvalidArtifactWithoutMutation(t *testing.T) {
	s := New()
	if _, err := s.CreateToolArtifact(domain.ToolArtifact{}, []byte("invalid")); err == nil {
		t.Fatal("invalid artifact accepted")
	}
	if len(s.data.ToolArtifacts) != 0 || len(s.artifactContent) != 0 {
		t.Fatal("invalid artifact changed state")
	}
}
