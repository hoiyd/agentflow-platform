package store

import (
	"errors"
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
	run, err := fileStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
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
	if _, err := workspaceA.ListToolEffects(run.ID); !IsNotFound(err) {
		t.Fatalf("cross-scope Tool effects must be rejected, got %v", err)
	}
	if _, _, _, err := workspaceA.CommitToolEffectReconciliation(domain.ToolEffectReconciliation{Event: domain.RunEvent{RunID: run.ID}}); !IsNotFound(err) {
		t.Fatalf("cross-scope Tool reconciliation must be rejected, got %v", err)
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

func TestWorkspaceStoreCoversRunOwnedMissingAndSuccessfulOperations(t *testing.T) {
	fileStore, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	scoped := fileStore.ForWorkspace(domain.NewWorkspaceScope("workspace-a"))

	if _, err := scoped.ListCollaborationSteps("missing"); !IsNotFound(err) {
		t.Fatalf("missing collaboration run: %v", err)
	}
	if replay, ok, err := scoped.GetRunReplay("missing"); err != nil || ok || replay.Run.ID != "" {
		t.Fatalf("missing replay should be hidden: replay=%#v ok=%t err=%v", replay, ok, err)
	}
	if ledger, ok, err := scoped.GetRunUsageLedger("missing"); err != nil || ok || ledger.RunID != "" {
		t.Fatalf("missing usage should be hidden: ledger=%#v ok=%t err=%v", ledger, ok, err)
	}
	if _, err := scoped.ListModelRequestRecords("missing"); !IsNotFound(err) {
		t.Fatalf("missing model requests: %v", err)
	}
	if _, err := scoped.UpdateRunVerificationStatus("missing", domain.VerificationPassed); !IsNotFound(err) {
		t.Fatalf("missing verification run: %v", err)
	}
	if documents, err := scoped.ListDocuments(); err != nil || len(documents) != 0 {
		t.Fatalf("list scoped documents: documents=%#v err=%v", documents, err)
	}

	conversation, err := scoped.CreateConversation("verification")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := fileStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	updated, err := scoped.UpdateRunVerificationStatus(run.ID, domain.VerificationPassed)
	if err != nil || updated.VerificationStatus != domain.VerificationPassed {
		t.Fatalf("update scoped verification: run=%#v err=%v", updated, err)
	}
}

func TestWorkspaceStorePropagatesRunLookupFailures(t *testing.T) {
	fileStore, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	want := errors.New("run lookup failed")
	scoped := workspaceStore{
		backend: &runLookupFailureStore{Store: fileStore, err: want}, workspaceID: "workspace-a",
	}

	if _, ok, err := scoped.GetRunReplay("run-1"); !errors.Is(err, want) || ok {
		t.Fatalf("replay lookup error: ok=%t err=%v", ok, err)
	}
	if _, ok, err := scoped.GetRunUsageLedger("run-1"); !errors.Is(err, want) || ok {
		t.Fatalf("usage lookup error: ok=%t err=%v", ok, err)
	}
	if _, err := scoped.ListModelRequestRecords("run-1"); !errors.Is(err, want) {
		t.Fatalf("model request lookup error: %v", err)
	}
	if _, err := scoped.UpdateRunVerificationStatus("run-1", domain.VerificationBlocked); !errors.Is(err, want) {
		t.Fatalf("verification lookup error: %v", err)
	}
}

func TestPostgresWorkspaceProviderCarriesNormalizedScope(t *testing.T) {
	scoped, ok := (&PostgresStore{}).ForWorkspace(domain.NewWorkspaceScope(" workspace-pg ")).(workspaceStore)
	if !ok || scoped.workspaceID != "workspace-pg" || scoped.backend == nil {
		t.Fatalf("unexpected Postgres Workspace store: %#v", scoped)
	}
}

type runLookupFailureStore struct {
	Store
	err error
}

func (s *runLookupFailureStore) GetRunInWorkspace(string, string) (domain.Run, bool, error) {
	return domain.Run{}, false, s.err
}
