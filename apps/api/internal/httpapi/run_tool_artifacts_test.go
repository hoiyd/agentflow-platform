package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
}
