package store

import (
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestWorkspaceStoreRejectsCrossScopeOwnedResources(t *testing.T) {
	fileStore, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := fileStore.CreateConversationInWorkspace("workspace-b", "private conversation")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := fileStore.CreateRun("agent_planner", conversation.ID, testRuntimeSnapshot())
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := fileStore.CreateRunEvent(domain.RunEvent{RunID: run.ID, Type: domain.EventRunCreated}); err != nil {
		t.Fatalf("create run event: %v", err)
	}

	workspaceA := fileStore.ForWorkspace(domain.NewWorkspaceScope("workspace-a"))
	if _, ok, err := workspaceA.GetRun(run.ID); err != nil || ok {
		t.Fatalf("cross-scope run lookup must be hidden: ok=%v err=%v", ok, err)
	}
	if _, err := workspaceA.ListRunEvents(run.ID); !IsNotFound(err) {
		t.Fatalf("cross-scope events must be rejected, got %v", err)
	}
	if _, err := workspaceA.CreateRunEvent(domain.RunEvent{RunID: run.ID, Type: domain.EventRunStarted}); !IsNotFound(err) {
		t.Fatalf("cross-scope event mutation must be rejected, got %v", err)
	}
	if _, err := workspaceA.AddMessageWithCitations(conversation.ID, "assistant", "private", nil); !IsNotFound(err) {
		t.Fatalf("cross-scope message mutation must be rejected, got %v", err)
	}

	workspaceB := fileStore.ForWorkspace(domain.NewWorkspaceScope("workspace-b"))
	events, err := workspaceB.ListRunEvents(run.ID)
	if err != nil || len(events) != 1 {
		t.Fatalf("owner Workspace should read run events: events=%d err=%v", len(events), err)
	}
}

func TestWorkspaceScopeAlwaysNormalizesToNonEmptyNamespace(t *testing.T) {
	if got := domain.NewWorkspaceScope("").ID(); got != domain.DefaultWorkspaceID {
		t.Fatalf("expected default Workspace scope, got %q", got)
	}
	if got := domain.NewWorkspaceScope(" workspace-a ").ID(); got != "workspace-a" {
		t.Fatalf("expected normalized Workspace scope, got %q", got)
	}
}
