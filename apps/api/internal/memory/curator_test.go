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

type immediateEmbedder struct{}

func (immediateEmbedder) EmbedText(context.Context, string) (openai.Embedding, error) {
	return openai.Embedding{Provider: "test", Model: "test", Vector: []float64{1}}, nil
}

func (e *blockingEmbedder) EmbedText(context.Context, string) (openai.Embedding, error) {
	e.once.Do(func() { close(e.started) })
	<-e.release
	return openai.Embedding{Provider: "test", Model: "test", Vector: []float64{1}}, e.err
}

type recordingStore struct {
	mu         sync.Mutex
	candidates []domain.MemoryCandidate
	memories   []domain.Memory
	events     []domain.RunEvent
}

func (s *recordingStore) CreateMemoryCandidate(candidate domain.MemoryCandidate) (domain.MemoryCandidate, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.candidates {
		if existing.ID == candidate.ID {
			return existing, false, nil
		}
	}
	s.candidates = append(s.candidates, candidate)
	return candidate, true, nil
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

func TestCuratorEnqueueDoesNotWaitAndFailureIsObservable(t *testing.T) {
	store := &recordingStore{}
	embedder := &blockingEmbedder{
		started: make(chan struct{}), release: make(chan struct{}), err: errors.New("embedding unavailable"),
	}
	curator := NewCuratorWithOptions(store, embedder, CuratorOptions{QueueSize: 4, JobTimeout: time.Second})

	if err := curator.Enqueue(CurationJob{RunID: "run_test", Message: domain.Message{
		ID: "msg_test", ConversationID: "conv_test", Role: "user",
		Content: "Remember that AgentFlow uses typed events.",
	}}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	select {
	case <-embedder.started:
	case <-time.After(time.Second):
		t.Fatal("curator did not start embedding")
	}

	close(embedder.release)
	closeCurator(t, curator)

	want := []domain.RunEventType{
		domain.EventMemoryCandidateProposed, domain.EventMemoryCandidateAccepted,
		domain.EventMemorySyncRequested, domain.EventMemorySyncFailed,
	}
	assertEventTypes(t, store.eventTypes(), want)
	if len(store.candidates) != 1 || store.candidates[0].Status != domain.MemoryCandidateAccepted {
		t.Fatalf("accepted candidate was not persisted: %#v", store.candidates)
	}
	if len(store.memories) != 0 {
		t.Fatalf("failed curation should not create memory: %#v", store.memories)
	}
}

func TestCuratorIgnoresOrdinaryChatAndAssistantOutput(t *testing.T) {
	store := &recordingStore{}
	embedder := &blockingEmbedder{started: make(chan struct{}), release: make(chan struct{})}
	curator := NewCuratorWithOptions(store, embedder, CuratorOptions{QueueSize: 4, JobTimeout: time.Second})
	if err := curator.Enqueue(CurationJob{RunID: "run_test", Message: domain.Message{
		ID: "user", Role: "user", Content: "Can you explain typed events?",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := curator.Enqueue(CurationJob{RunID: "run_test", Message: domain.Message{
		ID: "assistant", Role: "assistant", Content: "Remember that this answer is correct.",
	}}); err != nil {
		t.Fatal(err)
	}
	closeCurator(t, curator)
	if len(store.candidates) != 0 || len(store.memories) != 0 || len(store.events) != 0 {
		t.Fatalf("ordinary chat became durable memory: candidates=%#v memories=%#v events=%#v", store.candidates, store.memories, store.events)
	}
}

func TestCuratorPersistsAdaptiveCandidateInShadowModeWithoutCommittingMemory(t *testing.T) {
	store := &recordingStore{}
	model := &stubCandidateModel{response: `{"decision":"add","kind":"preference","content":"User prefers concise implementation notes.","confidence":0.94}`}
	curator := NewCuratorWithOptions(store, immediateEmbedder{}, CuratorOptions{
		QueueSize: 4, JobTimeout: time.Second, AdaptiveMode: AdaptiveModeShadow,
		Extractor: CompositeCandidateExtractor{
			Primary: RuleBasedCandidateExtractor{}, Fallback: AdaptiveCandidateExtractor{Model: model},
		},
	})
	if err := curator.Enqueue(CurationJob{RunID: "run_shadow", Message: domain.Message{
		ID: "msg_shadow", ConversationID: "conv_test", Role: "user",
		Content: "Concise implementation notes are much easier for me to review later.",
	}}); err != nil {
		t.Fatal(err)
	}
	closeCurator(t, curator)
	if len(store.candidates) != 1 || store.candidates[0].PolicyReason != PolicyRejectShadowMode || store.candidates[0].Confidence != 0.94 {
		t.Fatalf("adaptive shadow candidate mismatch: %#v", store.candidates)
	}
	if len(store.memories) != 0 {
		t.Fatalf("shadow candidate became durable memory: %#v", store.memories)
	}
	assertEventTypes(t, store.eventTypes(), []domain.RunEventType{
		domain.EventMemoryCandidateProposed, domain.EventMemoryCandidateRejected,
	})
}

func TestCuratorCommitsHighConfidenceAdaptiveCandidateInAutoMode(t *testing.T) {
	store := &recordingStore{}
	model := &stubCandidateModel{response: `{"decision":"add","kind":"project_convention","content":"The backend uses Go 1.26.5.","confidence":0.96}`}
	curator := NewCuratorWithOptions(store, immediateEmbedder{}, CuratorOptions{
		QueueSize: 4, JobTimeout: time.Second, AdaptiveMode: AdaptiveModeAuto,
		Extractor: AdaptiveCandidateExtractor{Model: model},
	})
	if err := curator.Enqueue(CurationJob{RunID: "run_auto", Message: domain.Message{
		ID: "msg_auto", ConversationID: "conv_test", Role: "user",
		Content: "All backend code in this project must use Go 1.26.5.",
	}}); err != nil {
		t.Fatal(err)
	}
	closeCurator(t, curator)
	if len(store.candidates) != 1 || store.candidates[0].Status != domain.MemoryCandidateAccepted {
		t.Fatalf("adaptive candidate was not accepted: %#v", store.candidates)
	}
	if len(store.memories) != 1 || store.memories[0].Content != "The backend uses Go 1.26.5." {
		t.Fatalf("adaptive memory mismatch: %#v", store.memories)
	}
}

func TestCuratorPublishesAdaptiveExtractionFailure(t *testing.T) {
	store := &recordingStore{}
	model := &stubCandidateModel{response: "not json"}
	curator := NewCuratorWithOptions(store, immediateEmbedder{}, CuratorOptions{
		QueueSize: 4, JobTimeout: time.Second, AdaptiveMode: AdaptiveModeShadow,
		Extractor: AdaptiveCandidateExtractor{Model: model},
	})
	if err := curator.Enqueue(CurationJob{RunID: "run_failed", Message: domain.Message{
		ID: "msg_failed", Role: "user", Content: "The backend always uses a typed event contract for observability.",
	}}); err != nil {
		t.Fatal(err)
	}
	closeCurator(t, curator)
	if len(store.candidates) != 0 || len(store.memories) != 0 {
		t.Fatalf("failed extraction persisted state: candidates=%#v memories=%#v", store.candidates, store.memories)
	}
	assertEventTypes(t, store.eventTypes(), []domain.RunEventType{domain.EventMemoryCandidateFailed})
}

func TestCuratorPersistsRejectedSensitiveCandidate(t *testing.T) {
	store := &recordingStore{}
	embedder := &blockingEmbedder{started: make(chan struct{}), release: make(chan struct{})}
	curator := NewCuratorWithOptions(store, embedder, CuratorOptions{QueueSize: 4, JobTimeout: time.Second})
	if err := curator.Enqueue(CurationJob{RunID: "run_test", Message: domain.Message{
		ID: "secret", Role: "user", Content: "Remember that my API key is sk-test-value.",
	}}); err != nil {
		t.Fatal(err)
	}
	closeCurator(t, curator)
	if len(store.candidates) != 1 || store.candidates[0].Status != domain.MemoryCandidateRejected || store.candidates[0].PolicyReason != PolicyRejectSecret {
		t.Fatalf("sensitive candidate was not rejected: %#v", store.candidates)
	}
	if store.candidates[0].Content != "[redacted potential secret]" {
		t.Fatalf("sensitive candidate content was persisted: %#v", store.candidates[0])
	}
	assertEventTypes(t, store.eventTypes(), []domain.RunEventType{domain.EventMemoryCandidateProposed, domain.EventMemoryCandidateRejected})
	if len(store.memories) != 0 {
		t.Fatalf("rejected candidate became memory: %#v", store.memories)
	}
}

func TestCuratorPreservesAcceptedCandidateOrder(t *testing.T) {
	store := &recordingStore{}
	embedder := &blockingEmbedder{started: make(chan struct{}), release: make(chan struct{})}
	curator := NewCuratorWithOptions(store, embedder, CuratorOptions{QueueSize: 4, JobTimeout: time.Second})
	for _, message := range []domain.Message{
		{ID: "first", Role: "user", Content: "Remember that the API is written in Go."},
		{ID: "second", Role: "user", Content: "I prefer concise engineering explanations."},
	} {
		if err := curator.Enqueue(CurationJob{RunID: "run_test", Message: message}); err != nil {
			t.Fatal(err)
		}
	}
	<-embedder.started
	close(embedder.release)
	closeCurator(t, curator)
	if len(store.memories) != 2 || store.memories[0].SourceMessageID != "first" || store.memories[1].SourceMessageID != "second" {
		t.Fatalf("queued candidates lost order: %#v", store.memories)
	}
}

func TestMemoryCurationFailureDoesNotChangeCompletedRun(t *testing.T) {
	fileStore, err := storepkg.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("memory failure")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := fileStore.CreateRunWithContract("agent_planner", conversation.ID, domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{},
	}, nil)

	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := fileStore.UpdateRunStatus(run.ID, domain.RunCompleted, ""); err != nil {
		t.Fatalf("complete run: %v", err)
	}
	embedder := &blockingEmbedder{
		started: make(chan struct{}), release: make(chan struct{}), err: errors.New("embedding unavailable"),
	}
	curator := NewCuratorWithOptions(fileStore, embedder, CuratorOptions{QueueSize: 4, JobTimeout: time.Second})
	if err := curator.Enqueue(CurationJob{RunID: run.ID, Message: domain.Message{
		ID: "msg_failed", ConversationID: conversation.ID, Role: "user",
		Content: "Remember that AgentFlow uses Postgres for durable state.",
	}}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	<-embedder.started
	close(embedder.release)
	closeCurator(t, curator)

	current, ok, err := fileStore.GetRun(run.ID)
	if err != nil || !ok {
		t.Fatalf("get run: ok=%v err=%v", ok, err)
	}
	if current.Status != domain.RunCompleted || current.Error != "" {
		t.Fatalf("memory failure changed primary run outcome: %#v", current)
	}
	replay, ok, err := fileStore.GetRunReplay(run.ID)
	if err != nil || !ok {
		t.Fatalf("run replay: ok=%v err=%v", ok, err)
	}
	if replay.Summary.ErrorCount != 1 {
		t.Fatalf("expected observable auxiliary failure, got %#v", replay.Summary)
	}
}

func closeCurator(t *testing.T, curator *Curator) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := curator.Close(ctx); err != nil {
		t.Fatalf("close curator: %v", err)
	}
}

func assertEventTypes(t *testing.T, got, want []domain.RunEventType) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("event types=%#v want=%#v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("event types=%#v want=%#v", got, want)
		}
	}
}
