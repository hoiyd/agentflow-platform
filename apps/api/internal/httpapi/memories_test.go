package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	memorypkg "agentflow-platform/apps/api/internal/memory"
	"agentflow-platform/apps/api/internal/store"
)

func TestMemoryCreateAndSearchAPI(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	client := newLocalFallbackOpenAIClientForTest()
	provider := memorypkg.NewBuiltinProvider(fileStore, client, memorypkg.ProviderOptions{})
	if err := provider.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize memory provider: %v", err)
	}
	defer closeMemoryProvider(t, provider)
	handler := &Handler{memories: provider}

	createRequest := httptest.NewRequest(http.MethodPost, "/api/memories", bytes.NewBufferString(`{
		"kind":"note","content":"AgentFlow uses typed run events."
	}`))
	createResponse := httptest.NewRecorder()
	handler.createMemory(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create memory: status=%d body=%s", createResponse.Code, createResponse.Body.String())
	}
	var created domain.Memory
	if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created memory: %v", err)
	}

	searchRequest := httptest.NewRequest(http.MethodPost, "/api/memories/search", bytes.NewBufferString(`{
		"query":"typed run events","limit":1
	}`))
	searchResponse := httptest.NewRecorder()
	handler.searchMemories(searchResponse, searchRequest)
	if searchResponse.Code != http.StatusOK {
		t.Fatalf("search memory: status=%d body=%s", searchResponse.Code, searchResponse.Body.String())
	}
	var items []domain.RetrievedMemory
	if err := json.Unmarshal(searchResponse.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode memory search: %v", err)
	}
	if len(items) != 1 || items[0].Memory.ID != created.ID {
		t.Fatalf("unexpected memory search result: %#v", items)
	}
}

func TestMemoryHandlersProjectDependencyFailures(t *testing.T) {
	want := errors.New("memory dependency failed")
	handler := &Handler{memories: memoryFailureOperations{err: want}}

	create := httptest.NewRequest(http.MethodPost, "/api/memories", bytes.NewBufferString(`{"kind":"note","content":"coverage"}`))
	assertHandlerFailure(t, handler.createMemory, create, http.StatusBadRequest)
	search := httptest.NewRequest(http.MethodPost, "/api/memories/search", bytes.NewBufferString(`{"query":"coverage"}`))
	assertHandlerFailure(t, handler.searchMemories, search, http.StatusBadRequest)
}

func TestExplicitUserMemoryCandidateCreatesSearchableMemory(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	client := newLocalFallbackOpenAIClientForTest()
	provider := memorypkg.NewBuiltinProvider(fileStore, client, memorypkg.ProviderOptions{})
	if err := provider.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize memory provider: %v", err)
	}
	handler := &Handler{store: fileStore, modelClient: client, memories: provider}
	conversation, err := fileStore.CreateConversation("memory sync")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := fileStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	message := domain.Message{
		ID:             "msg_test",
		ConversationID: conversation.ID,
		Role:           "user",
		Content:        "Remember that pgvector stores semantic memory embeddings.",
		CreatedAt:      time.Now().UTC(),
	}

	handler.syncMemoryTurn(message, run.ID)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := provider.Close(ctx); err != nil {
		t.Fatalf("drain memory provider: %v", err)
	}
	queryEmbedding, err := client.EmbedText(context.Background(), "pgvector semantic memory embeddings")
	if err != nil {
		t.Fatalf("embed query: %v", err)
	}
	items, err := fileStore.SearchMemories(domain.MemorySearch{
		Embedding: queryEmbedding.Vector,
		Metadata:  map[string]string{"source_role": "user"},
		Limit:     1,
	})
	if err != nil {
		t.Fatalf("search memories: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one memory, got %d", len(items))
	}
	if items[0].Memory.SourceMessageID != message.ID {
		t.Fatalf("expected source message %q, got %q", message.ID, items[0].Memory.SourceMessageID)
	}
	if items[0].Memory.RunID != run.ID {
		t.Fatalf("expected run id to be stored, got %q", items[0].Memory.RunID)
	}
	events, err := fileStore.ListRunEvents(run.ID)
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	wantEvents := []domain.RunEventType{
		domain.EventMemoryCandidateProposed, domain.EventMemoryCandidateAccepted,
		domain.EventMemorySyncRequested, domain.EventMemorySyncCompleted,
	}
	if len(events) != len(wantEvents) {
		t.Fatalf("unexpected memory synchronization events: %#v", events)
	}
	for index, want := range wantEvents {
		if events[index].Type != want {
			t.Fatalf("event[%d]=%s want=%s", index, events[index].Type, want)
		}
	}
	candidates, err := fileStore.ListMemoryCandidates(conversation.ID)
	if err != nil || len(candidates) != 1 || candidates[0].Status != domain.MemoryCandidateAccepted {
		t.Fatalf("accepted candidate was not persisted: candidates=%#v err=%v", candidates, err)
	}
}

type memoryFailureOperations struct{ err error }

func (s memoryFailureOperations) Commit(context.Context, domain.Memory) (domain.Memory, error) {
	return domain.Memory{}, s.err
}

func (s memoryFailureOperations) Recall(context.Context, domain.MemorySearch) ([]domain.RetrievedMemory, error) {
	return nil, s.err
}

func (s memoryFailureOperations) SyncTurn(memorypkg.TurnSyncRequest) error { return s.err }

func closeMemoryProvider(t *testing.T, provider *memorypkg.BuiltinProvider) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := provider.Close(ctx); err != nil {
		t.Fatalf("close memory provider: %v", err)
	}
}
