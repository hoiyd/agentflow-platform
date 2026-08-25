package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func TestTaskStateAPIProvidesPatchTimelineAndHistoricalVersion(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := fileStore.CreateConversationInWorkspace("workspace-a", "task state api")
	if err != nil {
		t.Fatal(err)
	}
	handler := (&Handler{store: fileStore}).Routes()
	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set(WorkspaceHeader, "workspace-a")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	}

	empty := request(http.MethodGet, "/api/conversations/"+conversation.ID+"/task-state", "")
	if empty.Code != http.StatusOK {
		t.Fatalf("get empty task state: status=%d body=%s", empty.Code, empty.Body.String())
	}
	var emptyState domain.TaskState
	if err := json.Unmarshal(empty.Body.Bytes(), &emptyState); err != nil || emptyState.Version != 0 || emptyState.WorkspaceID != "workspace-a" {
		t.Fatalf("empty state: state=%#v err=%v", emptyState, err)
	}

	patchBody := `{"expected_version":0,"operations":[{"type":"set_goal","goal":"Expose durable task state"},{"type":"upsert_task","task":{"id":"api","title":"Add API","status":"completed"}}]}`
	patched := request(http.MethodPatch, "/api/conversations/"+conversation.ID+"/task-state", patchBody)
	if patched.Code != http.StatusOK {
		t.Fatalf("patch task state: status=%d body=%s", patched.Code, patched.Body.String())
	}
	var revision domain.TaskStateRevision
	if err := json.Unmarshal(patched.Body.Bytes(), &revision); err != nil || revision.Version != 1 || revision.Source.ActorType != "user" {
		t.Fatalf("patch response: revision=%#v err=%v", revision, err)
	}

	stale := request(http.MethodPatch, "/api/conversations/"+conversation.ID+"/task-state", patchBody)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale patch status=%d body=%s", stale.Code, stale.Body.String())
	}
	var conflict apiErrorResponse
	if err := json.Unmarshal(stale.Body.Bytes(), &conflict); err != nil || conflict.Code != "task_state_version_conflict" {
		t.Fatalf("conflict response: %#v err=%v", conflict, err)
	}

	timeline := request(http.MethodGet, "/api/conversations/"+conversation.ID+"/task-state/revisions", "")
	var revisions []domain.TaskStateRevision
	if timeline.Code != http.StatusOK || json.Unmarshal(timeline.Body.Bytes(), &revisions) != nil || len(revisions) != 1 {
		t.Fatalf("timeline status=%d revisions=%#v body=%s", timeline.Code, revisions, timeline.Body.String())
	}
	historical := request(http.MethodGet, "/api/conversations/"+conversation.ID+"/task-state/revisions/1", "")
	if historical.Code != http.StatusOK || !bytes.Contains(historical.Body.Bytes(), []byte(`"version":1`)) {
		t.Fatalf("historical revision status=%d body=%s", historical.Code, historical.Body.String())
	}

	wrongWorkspace := httptest.NewRequest(http.MethodGet, "/api/conversations/"+conversation.ID+"/task-state", nil)
	wrongWorkspace.Header.Set(WorkspaceHeader, "workspace-b")
	wrongRecorder := httptest.NewRecorder()
	handler.ServeHTTP(wrongRecorder, wrongWorkspace)
	if wrongRecorder.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace state leaked: status=%d body=%s", wrongRecorder.Code, wrongRecorder.Body.String())
	}
}

func TestTaskStateAPIRejectsInvalidPatchAndVersion(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatal(err)
	}
	conversation, _ := fileStore.CreateConversation("invalid task state")
	handler := (&Handler{store: fileStore}).Routes()
	tests := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodPatch, path: "/api/conversations/" + conversation.ID + "/task-state", body: `{"expected_version":0,"operations":[],"unknown":true}`},
		{method: http.MethodPatch, path: "/api/conversations/" + conversation.ID + "/task-state", body: `{"expected_version":0,"operations":[]}`},
		{method: http.MethodPatch, path: "/api/conversations/" + conversation.ID + "/task-state", body: `{"expected_version":0,"operations":[{"type":"clear_goal"}]} {}`},
		{method: http.MethodGet, path: "/api/conversations/" + conversation.ID + "/task-state/revisions/not-a-version"},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(test.method, test.path, bytes.NewBufferString(test.body)))
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("%s %s: expected 400, got %d body=%s", test.method, test.path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestTaskStateUnknownStorageFailureReturnsInternalError(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/api/conversations/conversation/task-state", nil)
	writeTaskStateError(recorder, request, errors.New("persistence unavailable"))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected storage failure to return 500, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}
