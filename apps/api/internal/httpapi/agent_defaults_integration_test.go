package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// Failure inventory: omitted Executor cannot turn false into true. All flag
// combinations must survive internal/HTTP writes, DB reopen and Run capture.
// Existing stored values and frozen Runs must not be normalized into new defaults.
func TestAgentFlagsSurvivePersistenceAndCaptureWithoutExecutorMarker(t *testing.T) {
	url := pgfixture.DatabaseURL(t)
	storage, err := store.NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	workspace, err := storage.DefaultWorkspace(t.Context(), identity.SuperUserID)
	if err != nil {
		t.Fatal(err)
	}
	scoped := storage.ForWorkspace(domain.NewWorkspaceScope(workspace))
	var saved []domain.Agent
	for _, memory := range []bool{false, true} {
		for _, retrieval := range []bool{false, true} {
			for _, executor := range []string{"", domain.DefaultAgentExecutor} {
				created, err := scoped.CreateAgent(domain.Agent{Name: "Flags", MemoryEnabled: memory, RetrievalEnabled: retrieval, Executor: executor})
				if err != nil {
					t.Fatal(err)
				}
				if created.MemoryEnabled != memory || created.RetrievalEnabled != retrieval {
					t.Fatalf("creation changed explicit flags: %+v", created)
				}
				created.Executor = ""
				created, err = scoped.UpdateAgent(created)
				if err != nil {
					t.Fatal(err)
				}
				saved = append(saved, created)
				if created.MemoryEnabled != memory || created.RetrievalEnabled != retrieval {
					t.Fatalf("update changed explicit flags: %+v", created)
				}
			}
		}
	}
	if err := storage.Close(); err != nil {
		t.Fatal(err)
	}
	storage, err = store.NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	scoped = storage.ForWorkspace(domain.NewWorkspaceScope(workspace))
	runtime := newRuntimeForTest(agent.RuntimeOptions{Store: storage}, newHTTPFixtureClientForTest())
	for _, expected := range saved {
		got, found, err := scoped.GetAgent(expected.ID)
		if err != nil || !found || got.MemoryEnabled != expected.MemoryEnabled || got.RetrievalEnabled != expected.RetrievalEnabled {
			t.Fatalf("reload changed flags: %+v %v", got, err)
		}
		conversation, err := scoped.CreateConversation("Flag capture")
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := runtime.PrepareChatRunWithContract(t.Context(), got.ID, conversation.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		frozen := prepared.Run.RuntimeSnapshot.Agent
		if frozen.MemoryEnabled != expected.MemoryEnabled || frozen.RetrievalEnabled != expected.RetrievalEnabled {
			t.Fatalf("capture changed flags: %+v", frozen)
		}
	}
	dependencies := completeHandlerDependencies(t)
	dependencies.Store, dependencies.AgentRuntime, dependencies.Workspaces = storage, runtime, storage
	handler, err := NewHandler(dependencies)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"name":"Defaults","tools":[]}`, `{"name":"Disabled","tools":[],"memory_enabled":false,"retrieval_enabled":false}`} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/agents", strings.NewReader(body))
		request.Header.Set(WorkspaceHeader, workspace)
		handler.Routes().ServeHTTP(response, request)
		var created domain.Agent
		if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &created) != nil {
			t.Fatalf("HTTP create: %d %s", response.Code, response.Body.String())
		}
		want := created.Name == "Defaults"
		if created.MemoryEnabled != want || created.RetrievalEnabled != want {
			t.Fatalf("HTTP defaults/false changed: %+v", created)
		}
	}
}
