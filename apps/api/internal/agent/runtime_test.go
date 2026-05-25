package agent

import (
	"context"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/store"
)

func TestRetrieveContextRecordsReplayRetrievalEvent(t *testing.T) {
	ctx := context.Background()
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	client := openai.NewClientWithTimeout("", "", "test", time.Second)
	runtime := NewRuntime(fileStore, client, nil)

	conversation, err := fileStore.CreateConversation("Demo retrieval")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := fileStore.CreateRun("agent_planner", conversation.ID)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	memoryText := "The portfolio demo uses pgvector memory retrieval."
	memoryEmbedding, err := client.EmbedText(ctx, memoryText)
	if err != nil {
		t.Fatalf("embed memory: %v", err)
	}
	if _, err := fileStore.CreateMemory(domain.Memory{
		Kind:    "note",
		Content: memoryText,
	}, domain.MemoryEmbedding{
		Provider:   memoryEmbedding.Provider,
		Model:      memoryEmbedding.Model,
		Dimensions: memoryEmbedding.Dimensions,
		Embedding:  memoryEmbedding.Vector,
	}); err != nil {
		t.Fatalf("create memory: %v", err)
	}

	chunkText := "Replay should show the retrieved knowledge chunk used by the answer."
	chunkEmbedding, err := client.EmbedText(ctx, chunkText)
	if err != nil {
		t.Fatalf("embed chunk: %v", err)
	}
	if _, err := fileStore.CreateDocument(domain.Document{
		Title:      "Demo Knowledge",
		SourceType: "text",
		Content:    chunkText,
		Metadata:   map[string]any{"format": "text"},
	}, []domain.DocumentChunk{{
		Content:    chunkText,
		TokenCount: 9,
		Metadata:   map[string]any{"chunk_type": "paragraph"},
	}}, []domain.DocumentChunkEmbedding{{
		Provider:   chunkEmbedding.Provider,
		Model:      chunkEmbedding.Model,
		Dimensions: chunkEmbedding.Dimensions,
		Embedding:  chunkEmbedding.Vector,
	}}); err != nil {
		t.Fatalf("create document: %v", err)
	}

	memories, chunks := runtime.retrieveContext(ctx, run.ID, "pgvector memory retrieval and replay knowledge chunk")
	if len(memories) == 0 {
		t.Fatal("expected retrieved memories")
	}
	if len(chunks) == 0 {
		t.Fatal("expected retrieved document chunks")
	}

	replay, ok, err := fileStore.GetRunReplay(run.ID)
	if err != nil {
		t.Fatalf("get replay: %v", err)
	}
	if !ok {
		t.Fatal("expected replay")
	}
	for _, event := range replay.Events {
		if event.Type != domain.TraceRetrieval {
			continue
		}
		if event.Payload["memory_count"] != len(memories) {
			t.Fatalf("expected memory_count %d, got %#v", len(memories), event.Payload["memory_count"])
		}
		if event.Payload["chunk_count"] != len(chunks) {
			t.Fatalf("expected chunk_count %d, got %#v", len(chunks), event.Payload["chunk_count"])
		}
		if _, ok := event.Payload["retrieved_memories"]; !ok {
			t.Fatal("expected retrieved memories in retrieval payload")
		}
		if _, ok := event.Payload["retrieved_chunks"]; !ok {
			t.Fatal("expected retrieved chunks in retrieval payload")
		}
		return
	}
	t.Fatal("expected retrieval trace event")
}
