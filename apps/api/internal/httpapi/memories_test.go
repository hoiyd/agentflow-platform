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
	handler := &Handler{
		store:  fileStore,
		openAI: newLocalFallbackOpenAIClientForTest(),
	}
	message := domain.Message{
		ID:             "msg_test",
		ConversationID: "conv_test",
		Role:           "user",
		Content:        "Remember that pgvector stores semantic memory embeddings.",
		CreatedAt:      time.Now().UTC(),
	}

	if err := handler.rememberMessage(context.Background(), message, "run_test"); err != nil {
		t.Fatalf("remember message: %v", err)
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
	if items[0].Memory.RunID != "run_test" {
		t.Fatalf("expected run id to be stored, got %q", items[0].Memory.RunID)
	}
}
