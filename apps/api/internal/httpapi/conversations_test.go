package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func TestUpdateConversationTitleAPI(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("Initial title")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	handler := &Handler{store: fileStore}

	req := httptest.NewRequest(http.MethodPatch, "/api/conversations/"+conversation.ID, bytes.NewReader([]byte(`{"title":"Edited title"}`)))
	recorder := httptest.NewRecorder()
	handler.updateConversation(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected update status 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}

	var updated domain.Conversation
	if err := json.Unmarshal(recorder.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode conversation: %v", err)
	}
	if updated.Title != "Edited title" {
		t.Fatalf("expected edited title, got %q", updated.Title)
	}

	persisted, ok, err := fileStore.GetConversation(conversation.ID)
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if !ok || persisted.Title != "Edited title" {
		t.Fatalf("expected persisted edited title, got %#v ok=%v", persisted, ok)
	}
}

func TestUpdateConversationTitleAPIRejectsEmptyTitle(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("Initial title")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	handler := &Handler{store: fileStore}

	req := httptest.NewRequest(http.MethodPatch, "/api/conversations/"+conversation.ID, bytes.NewReader([]byte(`{"title":"   "}`)))
	recorder := httptest.NewRecorder()
	handler.updateConversation(recorder, req)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected update status 400, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestSummarizeConversationTitleBestEffortDoesNotOverwriteManualTitle(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("Manual title")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	handler := &Handler{store: fileStore}

	title := handler.summarizeConversationTitleBestEffort(
		httptest.NewRequest(http.MethodGet, "/", nil).Context(),
		conversation.ID,
		"Please explain vector search in RAG.",
		"Vector search retrieves semantically similar chunks.",
	)
	if title != "Manual title" {
		t.Fatalf("expected manual title to remain, got %q", title)
	}
}

func TestSummarizeConversationTitleBestEffortUpdatesTemporaryTitle(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("New conversation")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	handler := &Handler{store: fileStore}

	title := handler.summarizeConversationTitleBestEffort(
		httptest.NewRequest(http.MethodGet, "/", nil).Context(),
		conversation.ID,
		"Explain vector search in RAG",
		"Vector search retrieves semantically similar chunks.",
	)
	if title != "Explain vector search in RAG" {
		t.Fatalf("expected heuristic title, got %q", title)
	}

	persisted, ok, err := fileStore.GetConversation(conversation.ID)
	if err != nil {
		t.Fatalf("get conversation: %v", err)
	}
	if !ok || persisted.Title != title {
		t.Fatalf("expected persisted auto title, got %#v ok=%v", persisted, ok)
	}
}
