package store

import (
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func TestFileStoreMemorySearchUsesMetadataSimilarityAndRecency(t *testing.T) {
	store, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	now := time.Now().UTC()
	if _, err := store.CreateMemory(domain.Memory{
		Kind:      "note",
		Content:   "Use pgvector for semantic memory search.",
		Metadata:  map[string]any{"topic": "database"},
		CreatedAt: now.Add(-24 * time.Hour),
	}, domain.MemoryEmbedding{Provider: "openai_compatible", Model: "text-embedding-3-small", Embedding: []float64{1, 0}}); err != nil {
		t.Fatalf("create memory: %v", err)
	}
	if _, err := store.CreateMemory(domain.Memory{
		Kind:      "note",
		Content:   "Unrelated frontend styling note.",
		Metadata:  map[string]any{"topic": "frontend"},
		CreatedAt: now,
	}, domain.MemoryEmbedding{Provider: "local", Model: "local_hash_embedding", Embedding: []float64{0, 1}}); err != nil {
		t.Fatalf("create memory: %v", err)
	}

	items, err := store.SearchMemories(domain.MemorySearch{
		Query:             "pgvector memory",
		Embedding:         []float64{1, 0},
		EmbeddingProvider: "openai_compatible",
		EmbeddingModel:    "text-embedding-3-small",
		Metadata:          map[string]string{"topic": "database"},
		Limit:             3,
	})
	if err != nil {
		t.Fatalf("search memories: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one filtered memory, got %d", len(items))
	}
	if !strings.Contains(items[0].Memory.Content, "pgvector") {
		t.Fatalf("expected pgvector memory, got %#v", items[0])
	}
	if items[0].Similarity <= 0 || items[0].RecencyBoost <= 0 || items[0].Score <= items[0].Similarity {
		t.Fatalf("expected similarity plus recency boost, got %#v", items[0])
	}

	items, err = store.SearchMemories(domain.MemorySearch{
		Query:             "pgvector memory",
		Embedding:         []float64{1, 0},
		EmbeddingProvider: "local",
		EmbeddingModel:    "local_hash_embedding",
		Metadata:          map[string]string{"topic": "database"},
		Limit:             3,
	})
	if err != nil {
		t.Fatalf("search model mismatch memories: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no cross-model memories, got %d", len(items))
	}
}

func TestFileStoreMemoryCandidateRoundTripIsIdempotent(t *testing.T) {
	path := t.TempDir() + "/agentflow.json"
	first, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	candidate := domain.MemoryCandidate{
		ID: "memcand_test", ConversationID: "conv_test", RunID: "run_test",
		SourceMessageID: "msg_test", SourceRole: "user", Kind: "preference",
		Content: "concise answers", Status: domain.MemoryCandidateAccepted,
		ExtractionReason: "adaptive_model", PolicyReason: "accepted", Confidence: 0.91,
	}
	created, ok, err := first.CreateMemoryCandidate(candidate)
	if err != nil || !ok || created.ID != candidate.ID {
		t.Fatalf("create candidate: created=%#v ok=%v err=%v", created, ok, err)
	}
	if _, ok, err := first.CreateMemoryCandidate(candidate); err != nil || ok {
		t.Fatalf("duplicate candidate should be idempotent: ok=%v err=%v", ok, err)
	}
	second, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	items, err := second.ListMemoryCandidates("conv_test")
	if err != nil || len(items) != 1 || items[0].Content != candidate.Content || items[0].Confidence != candidate.Confidence {
		t.Fatalf("candidate round trip: items=%#v err=%v", items, err)
	}
}

func TestFileStoreMemoryWriteIsIdempotentByID(t *testing.T) {
	store, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	item := domain.Memory{ID: "mem_retry", Kind: "fact", Content: "first value"}
	if _, err := store.CreateMemory(item, domain.MemoryEmbedding{Provider: "test", Model: "v1", Embedding: []float64{1, 0}}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	item.Content = "retry value"
	if _, err := store.CreateMemory(item, domain.MemoryEmbedding{Provider: "test", Model: "v1", Embedding: []float64{0, 1}}); err != nil {
		t.Fatalf("retry write: %v", err)
	}
	if len(store.data.Memories) != 1 || len(store.data.MemoryEmbeddings) != 1 {
		t.Fatalf("retry created duplicate rows: memories=%d embeddings=%d", len(store.data.Memories), len(store.data.MemoryEmbeddings))
	}
	if store.data.Memories[0].Content != "retry value" || store.data.MemoryEmbeddings[0].Embedding[1] != 1 {
		t.Fatalf("retry did not upsert state: memory=%#v embedding=%#v", store.data.Memories[0], store.data.MemoryEmbeddings[0])
	}
}
