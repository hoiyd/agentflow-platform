package httpapi

import (
	"context"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func TestRememberMessageCreatesSearchableEmbedding(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	client := newLocalFallbackOpenAIClientForTest()
	handler := NewHandler(fileStore, client, nil, nil)
	conversation, err := fileStore.CreateConversation("memory sync")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := fileStore.CreateRun("agent_planner", conversation.ID)
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

	handler.enqueueMemorySync(message, run.ID)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := handler.Close(ctx); err != nil {
		t.Fatalf("drain memory sync: %v", err)
	}
	queryEmbedding, err := handler.openAI.EmbedText(context.Background(), "pgvector semantic memory embeddings")
	if err != nil {
		t.Fatalf("embed query: %v", err)
	}
	items, err := fileStore.SearchMemories(domain.MemorySearch{
		Embedding: queryEmbedding.Vector,
		Metadata:  map[string]string{"role": "user"},
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
	if len(events) != 2 || events[0].Type != domain.EventMemorySyncRequested || events[1].Type != domain.EventMemorySyncCompleted {
		t.Fatalf("unexpected memory sync events: %#v", events)
	}
}
