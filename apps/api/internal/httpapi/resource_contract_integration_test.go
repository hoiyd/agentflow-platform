package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/knowledge"
	memorypkg "agentflow-platform/apps/api/internal/memory"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// Failure inventory: absent operations/status schemas must fail; empty/null
// collections must match the wire; invalid ranges and stale mutations reject;
// foreign resource IDs return 404 and denied writes leave persisted state intact.
func TestResourceHTTPContracts(t *testing.T) {
	t.Run("fixture", func(t *testing.T) { resourceHTTPContracts(t, fixturestore.New(), "1", "2") })
	t.Run("postgres", func(t *testing.T) {
		backend, err := store.NewPostgresStore(pgfixture.DatabaseURL(t))
		if err != nil {
			t.Fatal(err)
		}
		defer backend.Close()
		owner, err := backend.CreateWorkspace(t.Context(), identity.SuperUserID, "Resource contracts", "")
		if err != nil {
			t.Fatal(err)
		}
		foreign, err := backend.CreateWorkspace(t.Context(), identity.SuperUserID, "Foreign resources", "")
		if err != nil {
			t.Fatal(err)
		}
		resourceHTTPContracts(t, backend, owner.ID, foreign.ID)
	})
}

func resourceHTTPContracts(t *testing.T, backend store.Store, workspace, foreign string) {
	t.Helper()
	contract := loadResourceContract(t)
	embedder := &metadataEmbedder{}
	memories := memorypkg.NewBuiltinProvider(backend, embedder, memorypkg.ProviderOptions{MaxAttempts: 1})
	if err := memories.Initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer closeMemoryProvider(t, memories)
	handler := (&Handler{store: backend, memories: memories, knowledge: knowledge.NewKnowledgeBase(backend, embedder)}).Routes()
	call := func(method, target, route, scope string, body []byte, contentType string, status int) []byte {
		t.Helper()
		request := httptest.NewRequest(method, target, bytes.NewReader(body))
		request.Header.Set(WorkspaceHeader, scope)
		request.Header.Set("Content-Type", contentType)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != status {
			t.Fatalf("%s %s: got %d want %d: %s", method, target, response.Code, status, response.Body.String())
		}
		contract.response(t, method, route, response)
		return response.Body.Bytes()
	}
	jsonCall := func(method, target, route, scope, body string, status int) []byte {
		t.Helper()
		if status < 300 && body != "" {
			contract.validate(t, "/paths/"+contractPointer(route)+"/"+strings.ToLower(method)+"/requestBody/content/application~1json/schema", []byte(body))
		}
		return call(method, target, route, scope, []byte(body), "application/json", status)
	}
	conversation, err := backend.CreateConversationInWorkspace(workspace, "Resource protocol")
	if err != nil {
		t.Fatal(err)
	}
	timeline := "/api/conversations/" + conversation.ID + "/task-state/revisions"
	revisionRoute := "/api/conversations/{id}/task-state/revisions"
	jsonCall("GET", timeline, revisionRoute, workspace, "", 200)
	_, err = backend.ForWorkspace(domain.NewWorkspaceScope(workspace)).ApplyTaskStatePatch(conversation.ID, domain.TaskStatePatch{
		ExpectedVersion: 0, Operations: []domain.TaskStateOperation{{Type: "set_goal", Goal: "Keep transport contracts"}},
	}, domain.TaskStateSource{ActorType: "user"})
	if err != nil {
		t.Fatal(err)
	}
	jsonCall("GET", timeline, revisionRoute, workspace, "", 200)
	jsonCall("GET", timeline+"/1", revisionRoute+"/{version}", workspace, "", 200)
	jsonCall("GET", timeline+"/invalid", revisionRoute+"/{version}", workspace, "", 400)
	jsonCall("GET", timeline+"/99", revisionRoute+"/{version}", workspace, "", 404)
	jsonCall("GET", timeline, revisionRoute, foreign, "", 404)
	jsonCall("GET", timeline+"/1", revisionRoute+"/{version}", foreign, "", 404)

	memoryBody := jsonCall("POST", "/api/memories", "/api/memories", workspace, `{"kind":"fact","content":"Contracts preserve source boundaries.","metadata":null}`, 201)
	var memory domain.Memory
	if err := json.Unmarshal(memoryBody, &memory); err != nil {
		t.Fatal(err)
	}
	memoryURL := "/api/memories/" + memory.ID
	jsonCall("POST", "/api/memories/search", "/api/memories/search", workspace, `{"query":"source boundaries"}`, 200)
	jsonCall("POST", "/api/memories/search", "/api/memories/search", foreign, `{"query":"source boundaries"}`, 200)
	jsonCall("GET", memoryURL, "/api/memories/{id}", workspace, "", 200)
	command := `{"operation_id":"correct","expected_version":1,"action":"replace","content":"Corrected source boundary.","actor":"user","reason":"correction"}`
	jsonCall("POST", memoryURL+"/mutations", "/api/memories/{id}/mutations", foreign, command, 404)
	jsonCall("POST", memoryURL+"/mutations", "/api/memories/{id}/mutations", workspace, command, 200)
	jsonCall("POST", memoryURL+"/mutations", "/api/memories/{id}/mutations", workspace, command, 200)
	jsonCall("POST", memoryURL+"/mutations", "/api/memories/{id}/mutations", workspace, strings.Replace(command, "correct\"", "stale\"", 1), 409)
	jsonCall("POST", memoryURL+"/mutations", "/api/memories/{id}/mutations", workspace, `{}`, 400)
	jsonCall("GET", memoryURL, "/api/memories/{id}", foreign, "", 404)
	jsonCall("POST", "/api/memories", "/api/memories", workspace, `{"kind":"fact","content":"bad scope","workspace_id":"`+foreign+`"}`, 400)
	jsonCall("POST", "/api/memories/search", "/api/memories/search", workspace, `{"query":"x","workspace_id":"`+foreign+`"}`, 400)

	jsonCall("GET", "/api/documents", "/api/documents", workspace, "", 200)
	upload, contentType := multipartUploadBody(t, "protocol.md", "# Protocol\nContracts preserve source boundaries.")
	documentBody := call("POST", "/api/documents/upload", "/api/documents/upload", workspace, upload.Bytes(), contentType, 201)
	var document domain.Document
	if err := json.Unmarshal(documentBody, &document); err != nil {
		t.Fatal(err)
	}
	jsonCall("GET", "/api/documents", "/api/documents", workspace, "", 200)
	badUpload, badType := multipartUploadBody(t, "unsupported.exe", "binary")
	call("POST", "/api/documents/upload", "/api/documents/upload", workspace, badUpload.Bytes(), badType, 400)
	documentURL := "/api/documents/" + document.ID
	detailBody := jsonCall("GET", documentURL, "/api/documents/{id}", workspace, "", 200)
	var detail struct {
		Document domain.Document  `json:"document"`
		Chunks   []map[string]any `json:"chunks"`
	}
	if err := json.Unmarshal(detailBody, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Document.ID != document.ID || len(detail.Chunks) == 0 {
		t.Fatalf("document detail lost identity: %s", detailBody)
	}
	for _, chunk := range detail.Chunks {
		if _, duplicated := chunk["document"]; duplicated {
			t.Fatal("chunk contains a duplicate Document")
		}
		if chunk["document_id"] != document.ID {
			t.Fatal("chunk lost document_id")
		}
	}
	jsonCall("GET", documentURL, "/api/documents/{id}", foreign, "", 404)
	jsonCall("DELETE", documentURL, "/api/documents/{id}", foreign, "", 404)
	if _, _, exists, err := backend.ForWorkspace(domain.NewWorkspaceScope(workspace)).GetDocument(document.ID); err != nil || !exists {
		t.Fatalf("foreign delete changed owner Document: %v", err)
	}
	jsonCall("DELETE", documentURL, "/api/documents/{id}", workspace, "", 204)
	jsonCall("DELETE", documentURL, "/api/documents/{id}", workspace, "", 404)

	run, err := backend.CreateRunWithContract("agent_planner", conversation.ID, domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	artifactURL := "/api/runs/" + run.ID + "/artifacts"
	artifactRoute := "/api/runs/{id}/artifacts"
	jsonCall("GET", artifactURL, artifactRoute, workspace, "", 200)
	content := []byte("bounded needle content")
	artifact := domain.ToolArtifact{ID: "contract-artifact", SchemaVersion: domain.CurrentToolArtifactSchemaVersion,
		RunID: run.ID, ToolCallID: "call", ToolName: "contract_probe", MediaType: "text/plain",
		ContentHash: store.ToolArtifactContentHash(content), OriginalByteSize: len(content), StoredByteSize: len(content), CreatedAt: time.Now().UTC()}
	if _, err := backend.CreateToolArtifact(artifact, content); err != nil {
		t.Fatal(err)
	}
	jsonCall("GET", artifactURL, artifactRoute, workspace, "", 200)
	readURL := artifactURL + "/" + artifact.ID
	readRoute := artifactRoute + "/{artifact_id}"
	jsonCall("GET", readURL+"?limit=8", readRoute, workspace, "", 200)
	jsonCall("GET", readURL+"/search?q=needle", readRoute+"/search", workspace, "", 200)
	jsonCall("GET", readURL+"/search?q=missing", readRoute+"/search", workspace, "", 200)
	jsonCall("GET", readURL+"?offset=-1", readRoute, workspace, "", 400)
	jsonCall("GET", readURL+"?offset=999999", readRoute, workspace, "", 416)
	jsonCall("GET", readURL+"/search", readRoute+"/search", workspace, "", 400)
	for _, entry := range []struct{ target, route string }{{artifactURL, artifactRoute}, {readURL, readRoute}, {readURL + "/search?q=needle", readRoute + "/search"}} {
		jsonCall("GET", entry.target, entry.route, foreign, "", 404)
	}
	expired := time.Now().Add(-time.Hour)
	artifact.ID, artifact.ExpiresAt = "expired-artifact", &expired
	if _, err := backend.CreateToolArtifact(artifact, content); err != nil {
		t.Fatal(err)
	}
	jsonCall("GET", artifactURL+"/"+artifact.ID, readRoute, workspace, "", 410)
	jsonCall("GET", artifactURL+"/"+artifact.ID+"/search?q=needle", readRoute+"/search", workspace, "", 410)
}
