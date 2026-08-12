package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func TestWorkspaceDefaultsUnscopedAPIRequests(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	handler := &Handler{store: fileStore}

	request := httptest.NewRequest(http.MethodGet, "/api/conversations", nil)
	recorder := httptest.NewRecorder()
	handler.Routes().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected missing workspace to use the reserved default, got %d body=%s", recorder.Code, recorder.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/conversations", nil)
	request.Header.Set(WorkspaceHeader, "workspace-a")
	recorder = httptest.NewRecorder()
	handler.Routes().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected scoped request to succeed, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestExplicitWorkspaceRejectsMismatchedPayload(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/rag/search", nil)
	request.Header.Set(WorkspaceHeader, "workspace-a")
	handler := &Handler{}
	recorder := httptest.NewRecorder()
	handler.withWorkspace(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := resolvePayloadWorkspace(r, "workspace-b"); ok {
			t.Fatal("expected mismatched payload workspace to be rejected")
		}
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("unexpected status %d", recorder.Code)
	}
}

func TestWorkspaceRejectsMismatchedHeaderAndQuery(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/conversations?workspace_id=workspace-b", nil)
	request.Header.Set(WorkspaceHeader, "workspace-a")
	recorder := httptest.NewRecorder()
	handler := &Handler{}
	handler.withWorkspace(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("request with conflicting workspace scopes reached the handler")
	})).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected conflicting workspace scopes to return 400, got %d", recorder.Code)
	}
}

func TestWorkspaceNormalizesLegacyDefaultID(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/conversations", nil)
	request.Header.Set(WorkspaceHeader, "default")
	recorder := httptest.NewRecorder()
	(&Handler{}).withWorkspace(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := workspaceIDFromRequest(r); got != domain.DefaultWorkspaceID {
			t.Fatalf("expected legacy default to normalize to %q, got %q", domain.DefaultWorkspaceID, got)
		}
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("unexpected status %d", recorder.Code)
	}
}
