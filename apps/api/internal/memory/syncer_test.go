package memory

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/openai"
	storepkg "agentflow-platform/apps/api/internal/store"
)

type blockingEmbedder struct {
	started chan struct{}
	release chan struct{}
	err     error
	once    sync.Once
}

func (e *blockingEmbedder) EmbedText(context.Context, string) (openai.Embedding, error) {
	e.once.Do(func() { close(e.started) })
	<-e.release
	return openai.Embedding{Provider: "test", Model: "test", Vector: []float64{1}}, e.err
}

type recordingStore struct {
	mu       sync.Mutex
	memories []domain.Memory
	events   []domain.RunEvent
}

func (s *recordingStore) CreateMemory(memory domain.Memory, _ domain.MemoryEmbedding) (domain.Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.memories = append(s.memories, memory)
	return memory, nil
}

func (s *recordingStore) CreateRunEvent(event domain.RunEvent) (domain.RunEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return event, nil
}

func (s *recordingStore) eventTypes() []domain.RunEventType {
	s.mu.Lock()
	defer s.mu.Unlock()
	types := make([]domain.RunEventType, 0, len(s.events))
	for _, event := range s.events {
		types = append(types, event.Type)
	}
	return types
}

func TestEnqueueDoesNotWaitForEmbeddingAndFailureIsObservable(t *testing.T) {
	store := &recordingStore{}
	embedder := &blockingEmbedder{
		started: make(chan struct{}), release: make(chan struct{}), err: errors.New("embedding unavailable"),
	}
	syncer := NewSyncerWithOptions(store, embedder, 4, time.Second)

	if err := syncer.Enqueue(Job{RunID: "run_test", Message: domain.Message{
		ID: "msg_test", ConversationID: "conv_test", Role: "user", Content: "remember this",
	}}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	select {
	case <-embedder.started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start embedding")
	}

	// Enqueue already returned even though the embedding call is still blocked.
	close(embedder.release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := syncer.Close(ctx); err != nil {
		t.Fatalf("close syncer: %v", err)
	}

	types := store.eventTypes()
	if len(types) != 2 || types[0] != domain.EventMemorySyncRequested || types[1] != domain.EventMemorySyncFailed {
		t.Fatalf("unexpected memory sync events: %#v", types)
	}
	if len(store.memories) != 0 {
		t.Fatalf("failed sync should not create memory: %#v", store.memories)
	}
}

func TestSyncerPreservesQueuedMessageOrder(t *testing.T) {
	store := &recordingStore{}
	embedder := &blockingEmbedder{started: make(chan struct{}), release: make(chan struct{})}
	syncer := NewSyncerWithOptions(store, embedder, 4, time.Second)
	if err := syncer.Enqueue(Job{RunID: "run_test", Message: domain.Message{ID: "first", Content: "one"}}); err != nil {
		t.Fatal(err)
	}
	if err := syncer.Enqueue(Job{RunID: "run_test", Message: domain.Message{ID: "second", Content: "two"}}); err != nil {
		t.Fatal(err)
	}
	<-embedder.started
	close(embedder.release)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := syncer.Close(ctx); err != nil {
		t.Fatalf("close syncer: %v", err)
	}
	if len(store.memories) != 2 || store.memories[0].SourceMessageID != "first" || store.memories[1].SourceMessageID != "second" {
		t.Fatalf("queued jobs lost order: %#v", store.memories)
	}
}

func TestMemorySyncFailureDoesNotChangeCompletedRun(t *testing.T) {
	fileStore, err := storepkg.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("memory failure")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := fileStore.CreateRun("agent_planner", conversation.ID)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := fileStore.UpdateRunStatus(run.ID, domain.RunCompleted, ""); err != nil {
		t.Fatalf("complete run: %v", err)
	}
	embedder := &blockingEmbedder{
		started: make(chan struct{}), release: make(chan struct{}), err: errors.New("embedding unavailable"),
	}
	syncer := NewSyncerWithOptions(fileStore, embedder, 4, time.Second)
	if err := syncer.Enqueue(Job{RunID: run.ID, Message: domain.Message{
		ID: "msg_failed", ConversationID: conversation.ID, Role: "assistant", Content: "answer already delivered",
	}}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	<-embedder.started
	close(embedder.release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := syncer.Close(ctx); err != nil {
		t.Fatalf("close syncer: %v", err)
	}

	current, ok, err := fileStore.GetRun(run.ID)
	if err != nil || !ok {
		t.Fatalf("get run: ok=%v err=%v", ok, err)
	}
	if current.Status != domain.RunCompleted || current.Error != "" {
		t.Fatalf("memory failure changed primary run outcome: %#v", current)
	}
	summary, err := fileStore.GetRunTraceSummary(run.ID)
	if err != nil {
		t.Fatalf("trace summary: %v", err)
	}
	if summary.ErrorCount != 1 {
		t.Fatalf("expected observable auxiliary failure, got %#v", summary)
	}
}
