package app

import (
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// Historical failure #167: the production Workspace wrapper hid optional
// Artifact reads. Check list/read/search, foreign scope and database reopen.
func TestObservableWorkspacePreservesArtifactReads(t *testing.T) {
	url := pgfixture.DatabaseURL(t)
	backend, err := store.NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { backend.Close() })
	if err := backend.InitializeWorkspaceLifecycle(t.Context()); err != nil {
		t.Fatal(err)
	}
	space, err := backend.CreateWorkspace(t.Context(), identity.SuperUserID, "Artifact owner", "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := backend.CreateWorkspace(t.Context(), identity.SuperUserID, "Other scope", "")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := backend.CreateConversationInWorkspace(space.ID, "Synthetic Artifact regression")
	if err != nil {
		t.Fatal(err)
	}
	run, err := backend.CreateRunWithContract("agent_planner", conversation.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("Synthetic retained artifact needle")
	expires := time.Now().Add(time.Hour)
	artifact, err := backend.CreateToolArtifact(domain.ToolArtifact{
		ID: "artifact-wrapper", SchemaVersion: domain.CurrentToolArtifactSchemaVersion, RunID: run.ID,
		ToolCallID: "call-wrapper", ToolName: "fixture_tool", MediaType: "text/plain",
		ContentHash: store.ToolArtifactContentHash(content), OriginalByteSize: len(content), StoredByteSize: len(content),
		CreatedAt: time.Now(), ExpiresAt: &expires,
	}, content)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		observed := newObservableStore(backend, event.NewHub(1))
		for _, scope := range []string{space.ID, other.ID} {
			reader, ok := observed.ForWorkspace(domain.NewWorkspaceScope(scope)).(interface {
				ListToolArtifacts(string) ([]domain.ToolArtifact, error)
				ReadToolArtifact(string, string, int, int) (domain.ToolArtifactRead, error)
				SearchToolArtifact(string, string, string, int) (domain.ToolArtifactSearchResult, error)
			})
			if !ok {
				t.Fatal("production Workspace wrapper lost Artifact capabilities")
			}
			items, listErr := reader.ListToolArtifacts(run.ID)
			read, readErr := reader.ReadToolArtifact(run.ID, artifact.ID, 0, len(content))
			search, searchErr := reader.SearchToolArtifact(run.ID, artifact.ID, "needle", 5)
			if scope == other.ID {
				if !store.IsNotFound(listErr) || !store.IsNotFound(readErr) || !store.IsNotFound(searchErr) {
					t.Fatalf("foreign Artifact became visible: list=%v read=%v search=%v", listErr, readErr, searchErr)
				}
				continue
			}
			if listErr != nil || readErr != nil || searchErr != nil || len(items) != 1 || items[0].ID != artifact.ID || !read.Complete || read.Content != string(content) || len(search.Matches) != 1 {
				t.Fatalf("wrapped Artifact changed after reopen=%t: list=%v read=%+v search=%+v errors=%v/%v/%v", attempt > 0, items, read, search, listErr, readErr, searchErr)
			}
		}
		if attempt == 0 {
			if err := backend.Close(); err != nil {
				t.Fatal(err)
			}
			backend, err = store.NewPostgresStore(url)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}
