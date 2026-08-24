package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestConversationCollectionMessagesAndDeleteAPI(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	handler := &Handler{store: fileStore}

	listRecorder := httptest.NewRecorder()
	handler.listConversations(listRecorder, httptest.NewRequest(http.MethodGet, "/api/conversations", nil))
	if listRecorder.Code != http.StatusOK || strings.TrimSpace(listRecorder.Body.String()) != "[]" {
		t.Fatalf("unexpected empty list response: status=%d body=%s", listRecorder.Code, listRecorder.Body.String())
	}

	createRecorder := httptest.NewRecorder()
	handler.createConversation(createRecorder, httptest.NewRequest(http.MethodPost, "/api/conversations", bytes.NewReader([]byte(`{"title":"Coverage conversation"}`))))
	if createRecorder.Code != http.StatusCreated {
		t.Fatalf("create conversation status=%d body=%s", createRecorder.Code, createRecorder.Body.String())
	}
	var conversation domain.Conversation
	if err := json.Unmarshal(createRecorder.Body.Bytes(), &conversation); err != nil {
		t.Fatalf("decode conversation: %v", err)
	}
	if _, err := fileStore.AddMessage(conversation.ID, "user", "hello"); err != nil {
		t.Fatalf("add message: %v", err)
	}

	messagesRecorder := httptest.NewRecorder()
	handler.listMessages(messagesRecorder, httptest.NewRequest(http.MethodGet, "/api/conversations/"+conversation.ID+"/messages", nil))
	if messagesRecorder.Code != http.StatusOK {
		t.Fatalf("list messages status=%d body=%s", messagesRecorder.Code, messagesRecorder.Body.String())
	}
	var messages []domain.Message
	if err := json.Unmarshal(messagesRecorder.Body.Bytes(), &messages); err != nil || len(messages) != 1 || messages[0].Content != "hello" {
		t.Fatalf("unexpected messages: items=%#v err=%v", messages, err)
	}

	deleteRecorder := httptest.NewRecorder()
	handler.deleteConversation(deleteRecorder, httptest.NewRequest(http.MethodDelete, "/api/conversations/"+conversation.ID, nil))
	if deleteRecorder.Code != http.StatusOK || !strings.Contains(deleteRecorder.Body.String(), `"deleted":true`) {
		t.Fatalf("delete conversation status=%d body=%s", deleteRecorder.Code, deleteRecorder.Body.String())
	}

	missingRecorder := httptest.NewRecorder()
	handler.listMessages(missingRecorder, httptest.NewRequest(http.MethodGet, "/api/conversations/"+conversation.ID+"/messages", nil))
	if missingRecorder.Code != http.StatusNotFound {
		t.Fatalf("expected deleted conversation status 404, got %d", missingRecorder.Code)
	}
}

func TestConversationHandlersRejectInvalidResourceIDs(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	handler := &Handler{store: fileStore}
	tests := []struct {
		name   string
		invoke func(http.ResponseWriter, *http.Request)
		method string
		path   string
	}{
		{name: "update", invoke: handler.updateConversation, method: http.MethodPatch, path: "/api/conversations/"},
		{name: "delete", invoke: handler.deleteConversation, method: http.MethodDelete, path: "/api/conversations/extra/path"},
		{name: "messages", invoke: handler.listMessages, method: http.MethodGet, path: "/api/conversations//messages"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			test.invoke(recorder, httptest.NewRequest(test.method, test.path, nil))
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("expected status 400, got %d body=%s", recorder.Code, recorder.Body.String())
			}
		})
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
		fileStore.ForWorkspace(domain.NewWorkspaceScope(domain.DefaultWorkspaceID)),
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
		fileStore.ForWorkspace(domain.NewWorkspaceScope(domain.DefaultWorkspaceID)),
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
