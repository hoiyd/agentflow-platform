package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func TestToolArtifactHandlersListReadSearchAndEnforceWorkspace(t *testing.T) {
	fileStore, err := store.NewFileStore(filepath.Join(t.TempDir(), "agentflow.json"))
	if err != nil {
		t.Fatal(err)
	}
	conversation, _ := fileStore.CreateConversationInWorkspace("workspace-a", "artifacts")
	run, err := fileStore.CreateRunWithContract("agent_planner", conversation.ID, domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte(`{"result":"bounded needle content"}`)
	sum := sha256.Sum256(content)
	expires := time.Now().UTC().Add(time.Hour)
	artifact := domain.ToolArtifact{
		ID: "tool_artifact_http", SchemaVersion: domain.CurrentToolArtifactSchemaVersion,
		RunID: run.ID, ToolCallID: "call-1", ToolName: "future_tool", MediaType: "application/json",
		ContentHash: "sha256:" + hex.EncodeToString(sum[:]), OriginalByteSize: len(content), StoredByteSize: len(content),
		CreatedAt: time.Now().UTC(), ExpiresAt: &expires,
	}
	if _, err := fileStore.CreateToolArtifact(artifact, content); err != nil {
		t.Fatal(err)
	}
	handler := &Handler{store: fileStore}

	listRequest := httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/artifacts", nil)
	listRequest.Header.Set(WorkspaceHeader, "workspace-a")
	listRequest.SetPathValue("id", run.ID)
	listResponse := httptest.NewRecorder()
	handler.listToolArtifacts(listResponse, listRequest)
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), artifact.ID) {
		t.Fatalf("list artifacts: status=%d body=%s", listResponse.Code, listResponse.Body.String())
	}

	readRequest := httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/artifacts/"+artifact.ID+"?limit=8", nil)
	readRequest.Header.Set(WorkspaceHeader, "workspace-a")
	readRequest.SetPathValue("id", run.ID)
	readRequest.SetPathValue("artifact_id", artifact.ID)
	readResponse := httptest.NewRecorder()
	handler.readToolArtifact(readResponse, readRequest)
	var read domain.ToolArtifactRead
	if err := json.Unmarshal(readResponse.Body.Bytes(), &read); err != nil || readResponse.Code != http.StatusOK || read.NextOffset != 8 {
		t.Fatalf("read artifact: status=%d result=%#v err=%v", readResponse.Code, read, err)
	}

	searchRequest := httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/artifacts/"+artifact.ID+"/search?q=needle", nil)
	searchRequest.Header.Set(WorkspaceHeader, "workspace-a")
	searchRequest.SetPathValue("id", run.ID)
	searchRequest.SetPathValue("artifact_id", artifact.ID)
	searchResponse := httptest.NewRecorder()
	handler.searchToolArtifact(searchResponse, searchRequest)
	if searchResponse.Code != http.StatusOK || !strings.Contains(searchResponse.Body.String(), "needle") {
		t.Fatalf("search artifact: status=%d body=%s", searchResponse.Code, searchResponse.Body.String())
	}

	foreignRequest := httptest.NewRequest(http.MethodGet, "/api/runs/"+run.ID+"/artifacts", nil)
	foreignRequest.Header.Set(WorkspaceHeader, "workspace-b")
	foreignRequest.SetPathValue("id", run.ID)
	foreignResponse := httptest.NewRecorder()
	handler.listToolArtifacts(foreignResponse, foreignRequest)
	if foreignResponse.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace artifact list status = %d, want 404", foreignResponse.Code)
	}
	for _, invoke := range []struct {
		name   string
		target string
		handle func(http.ResponseWriter, *http.Request)
	}{
		{name: "read", target: "/api/runs/" + run.ID + "/artifacts/" + artifact.ID, handle: handler.readToolArtifact},
		{name: "search", target: "/api/runs/" + run.ID + "/artifacts/" + artifact.ID + "/search?q=needle", handle: handler.searchToolArtifact},
	} {
		t.Run("cross workspace "+invoke.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, invoke.target, nil)
			request.Header.Set(WorkspaceHeader, "workspace-b")
			request.SetPathValue("id", run.ID)
			request.SetPathValue("artifact_id", artifact.ID)
			response := httptest.NewRecorder()
			invoke.handle(response, request)
			if response.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404; body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestToolArtifactHandlersRejectInvalidQueries(t *testing.T) {
	fileStore, err := store.NewFileStore(filepath.Join(t.TempDir(), "agentflow.json"))
	if err != nil {
		t.Fatal(err)
	}
	handler := &Handler{store: fileStore}
	tests := []struct {
		name   string
		target string
		handle func(http.ResponseWriter, *http.Request)
	}{
		{name: "read offset is not an integer", target: "/api/runs/run-1/artifacts/a?offset=nope", handle: handler.readToolArtifact},
		{name: "read limit is not an integer", target: "/api/runs/run-1/artifacts/a?limit=nope", handle: handler.readToolArtifact},
		{name: "read offset is negative", target: "/api/runs/run-1/artifacts/a?offset=-1", handle: handler.readToolArtifact},
		{name: "read limit is zero", target: "/api/runs/run-1/artifacts/a?limit=0", handle: handler.readToolArtifact},
		{name: "read limit is too large", target: "/api/runs/run-1/artifacts/a?limit=65537", handle: handler.readToolArtifact},
		{name: "search max matches is not an integer", target: "/api/runs/run-1/artifacts/a/search?q=needle&max_matches=nope", handle: handler.searchToolArtifact},
		{name: "search query is empty", target: "/api/runs/run-1/artifacts/a/search", handle: handler.searchToolArtifact},
		{name: "search query is too large", target: "/api/runs/run-1/artifacts/a/search?q=" + strings.Repeat("x", store.MaxToolArtifactSearchQuery+1), handle: handler.searchToolArtifact},
		{name: "search max matches is zero", target: "/api/runs/run-1/artifacts/a/search?q=needle&max_matches=0", handle: handler.searchToolArtifact},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.target, nil)
			request.SetPathValue("id", "run-1")
			request.SetPathValue("artifact_id", "a")
			response := httptest.NewRecorder()
			test.handle(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestToolArtifactHandlersMapStorageFailures(t *testing.T) {
	base, err := store.NewFileStore(filepath.Join(t.TempDir(), "agentflow.json"))
	if err != nil {
		t.Fatal(err)
	}
	baseWorkspace := base.ForWorkspace(domain.NewWorkspaceScope(domain.DefaultWorkspaceID))

	unavailable := &Handler{store: &toolArtifactHTTPStore{
		Store:     base,
		workspace: &workspaceWithoutToolArtifacts{WorkspaceStore: baseWorkspace},
	}}
	request := artifactHandlerRequest("/api/runs/run-1/artifacts")
	response := httptest.NewRecorder()
	unavailable.listToolArtifacts(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable artifact store status = %d", response.Code)
	}

	tests := []struct {
		name     string
		err      error
		expected int
	}{
		{name: "not found", err: store.ErrNotFound("tool artifact"), expected: http.StatusNotFound},
		{name: "expired", err: store.ErrToolArtifactExpired, expected: http.StatusGone},
		{name: "range", err: store.ErrToolArtifactRange, expected: http.StatusRequestedRangeNotSatisfiable},
		{name: "internal", err: errors.New("storage unavailable"), expected: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workspace := &failingToolArtifactWorkspace{WorkspaceStore: baseWorkspace, err: test.err}
			handler := &Handler{store: &toolArtifactHTTPStore{Store: base, workspace: workspace}}
			for _, invoke := range []struct {
				name   string
				target string
				handle func(http.ResponseWriter, *http.Request)
			}{
				{name: "list", target: "/api/runs/run-1/artifacts", handle: handler.listToolArtifacts},
				{name: "read", target: "/api/runs/run-1/artifacts/a", handle: handler.readToolArtifact},
				{name: "search", target: "/api/runs/run-1/artifacts/a/search?q=needle", handle: handler.searchToolArtifact},
			} {
				t.Run(invoke.name, func(t *testing.T) {
					response := httptest.NewRecorder()
					invoke.handle(response, artifactHandlerRequest(invoke.target))
					if response.Code != test.expected {
						t.Fatalf("status = %d, want %d; body=%s", response.Code, test.expected, response.Body.String())
					}
				})
			}
		})
	}
}

func artifactHandlerRequest(target string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.SetPathValue("id", "run-1")
	request.SetPathValue("artifact_id", "a")
	return request
}

type toolArtifactHTTPStore struct {
	store.Store
	workspace store.WorkspaceStore
}

func (s *toolArtifactHTTPStore) ForWorkspace(domain.WorkspaceScope) store.WorkspaceStore {
	return s.workspace
}

type workspaceWithoutToolArtifacts struct {
	store.WorkspaceStore
}

type failingToolArtifactWorkspace struct {
	store.WorkspaceStore
	err error
}

func (s *failingToolArtifactWorkspace) ListToolArtifacts(string) ([]domain.ToolArtifact, error) {
	return nil, s.err
}

func (s *failingToolArtifactWorkspace) ReadToolArtifact(string, string, int, int) (domain.ToolArtifactRead, error) {
	return domain.ToolArtifactRead{}, s.err
}

func (s *failingToolArtifactWorkspace) SearchToolArtifact(string, string, string, int) (domain.ToolArtifactSearchResult, error) {
	return domain.ToolArtifactSearchResult{}, s.err
}
