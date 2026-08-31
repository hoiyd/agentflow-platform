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

func (s *recordingStore) SearchMemories(domain.MemorySearch) ([]domain.RetrievedMemory, error) {
	return nil, nil
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

func (s *recordingStore) counts() (int, int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.candidates), len(s.memories), len(s.events)
}

func TestProviderSyncTurnDoesNotWaitAndFailureIsObservable(t *testing.T) {
	store := &recordingStore{}
	embedder := &blockingEmbedder{
		started: make(chan struct{}), release: make(chan struct{}), err: errors.New("embedding unavailable"),
	}
	provider := newTestProvider(t, store, embedder, ProviderOptions{QueueSize: 4, JobTimeout: time.Second})

	if err := provider.SyncTurn(TurnSyncRequest{RunID: "run_test", Message: domain.Message{
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
	closeProvider(t, provider)

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

func TestProviderSyncTurnIgnoresOrdinaryChatAndAssistantOutput(t *testing.T) {
	store := &recordingStore{}
	embedder := &blockingEmbedder{started: make(chan struct{}), release: make(chan struct{})}
	provider := newTestProvider(t, store, embedder, ProviderOptions{QueueSize: 4, JobTimeout: time.Second})
	if err := provider.SyncTurn(TurnSyncRequest{RunID: "run_test", Message: domain.Message{
		ID: "user", Role: "user", Content: "Can you explain typed events?",
	}}); err != nil {
		t.Fatal(err)
	}
	if err := provider.SyncTurn(TurnSyncRequest{RunID: "run_test", Message: domain.Message{
		ID: "assistant", Role: "assistant", Content: "Remember that this answer is correct.",
	}}); err != nil {
		t.Fatal(err)
	}
	closeProvider(t, provider)
	if len(store.candidates) != 0 || len(store.memories) != 0 || len(store.events) != 0 {
		t.Fatalf("ordinary chat became durable memory: candidates=%#v memories=%#v events=%#v", store.candidates, store.memories, store.events)
	}
}

func TestProviderPersistsAdaptiveCandidateInShadowModeWithoutCommittingMemory(t *testing.T) {
	store := &recordingStore{}
	model := &stubCandidateModel{response: `{"decision":"add","kind":"preference","content":"User prefers concise implementation notes.","confidence":0.94}`}
	provider := newTestProvider(t, store, immediateEmbedder{}, ProviderOptions{
		QueueSize: 4, JobTimeout: time.Second, AdaptiveMode: AdaptiveModeShadow,
		Extractor: CompositeCandidateExtractor{
			Primary: RuleBasedCandidateExtractor{}, Fallback: AdaptiveCandidateExtractor{Model: model},
		},
	})
	if err := provider.SyncTurn(TurnSyncRequest{RunID: "run_shadow", Message: domain.Message{
		ID: "msg_shadow", ConversationID: "conv_test", Role: "user",
		Content: "Concise implementation notes are much easier for me to review later.",
	}}); err != nil {
		t.Fatal(err)
	}
	closeProvider(t, provider)
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

func TestProviderCommitsHighConfidenceAdaptiveCandidateInAutoMode(t *testing.T) {
	store := &recordingStore{}
	model := &stubCandidateModel{response: `{"decision":"add","kind":"project_convention","content":"The backend uses Go 1.26.5.","confidence":0.96}`}
	provider := newTestProvider(t, store, immediateEmbedder{}, ProviderOptions{
		QueueSize: 4, JobTimeout: time.Second, AdaptiveMode: AdaptiveModeAuto,
		Extractor: AdaptiveCandidateExtractor{Model: model},
	})
	if err := provider.SyncTurn(TurnSyncRequest{RunID: "run_auto", Message: domain.Message{
		ID: "msg_auto", ConversationID: "conv_test", Role: "user",
		Content: "All backend code in this project must use Go 1.26.5.",
	}}); err != nil {
		t.Fatal(err)
	}
	closeProvider(t, provider)
	if len(store.candidates) != 1 || store.candidates[0].Status != domain.MemoryCandidateAccepted {
		t.Fatalf("adaptive candidate was not accepted: %#v", store.candidates)
	}
	if len(store.memories) != 1 || store.memories[0].Content != "The backend uses Go 1.26.5." {
		t.Fatalf("adaptive memory mismatch: %#v", store.memories)
	}
}

func TestProviderPublishesAdaptiveExtractionFailure(t *testing.T) {
	store := &recordingStore{}
	model := &stubCandidateModel{response: "not json"}
	provider := newTestProvider(t, store, immediateEmbedder{}, ProviderOptions{
		QueueSize: 4, JobTimeout: time.Second, AdaptiveMode: AdaptiveModeShadow,
		Extractor: AdaptiveCandidateExtractor{Model: model},
	})
	if err := provider.SyncTurn(TurnSyncRequest{RunID: "run_failed", Message: domain.Message{
		ID: "msg_failed", Role: "user", Content: "The backend always uses a typed event contract for observability.",
	}}); err != nil {
		t.Fatal(err)
	}
	closeProvider(t, provider)
	if len(store.candidates) != 0 || len(store.memories) != 0 {
		t.Fatalf("failed extraction persisted state: candidates=%#v memories=%#v", store.candidates, store.memories)
	}
	assertEventTypes(t, store.eventTypes(), []domain.RunEventType{domain.EventMemoryCandidateFailed})
}

func TestProviderPersistsRejectedSensitiveCandidate(t *testing.T) {
	store := &recordingStore{}
	embedder := &blockingEmbedder{started: make(chan struct{}), release: make(chan struct{})}
	provider := newTestProvider(t, store, embedder, ProviderOptions{QueueSize: 4, JobTimeout: time.Second})
	if err := provider.SyncTurn(TurnSyncRequest{RunID: "run_test", Message: domain.Message{
		ID: "secret", Role: "user", Content: "Remember that my API key is sk-test-value.",
	}}); err != nil {
		t.Fatal(err)
	}
	closeProvider(t, provider)
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

func TestProviderPreservesAcceptedTurnOrder(t *testing.T) {
	store := &recordingStore{}
	embedder := &blockingEmbedder{started: make(chan struct{}), release: make(chan struct{})}
	provider := newTestProvider(t, store, embedder, ProviderOptions{QueueSize: 4, JobTimeout: time.Second})
	for _, message := range []domain.Message{
		{ID: "first", Role: "user", Content: "Remember that the API is written in Go."},
		{ID: "second", Role: "user", Content: "I prefer concise engineering explanations."},
	} {
		if err := provider.SyncTurn(TurnSyncRequest{RunID: "run_test", Message: message}); err != nil {
			t.Fatal(err)
		}
	}
	<-embedder.started
	close(embedder.release)
	closeProvider(t, provider)
	if len(store.memories) != 2 || store.memories[0].SourceMessageID != "first" || store.memories[1].SourceMessageID != "second" {
		t.Fatalf("queued candidates lost order: %#v", store.memories)
	}
}

func TestProviderSyncTurnIsIdempotentAfterWorkerCompletion(t *testing.T) {
	store := &recordingStore{}
	provider := newTestProvider(t, store, immediateEmbedder{}, ProviderOptions{QueueSize: 2, JobTimeout: time.Second})
	request := TurnSyncRequest{RunID: "run_retry", IdempotencyKey: "run_retry:turn_1", Message: domain.Message{
		ID: "msg_retry", ConversationID: "conv_retry", Role: "user",
		Content: "Remember that durable turn sync must be idempotent.",
	}}
	if err := provider.SyncTurn(request); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		_, memories, _ := store.counts()
		if memories == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first sync did not commit memory")
		}
		time.Sleep(time.Millisecond)
	}
	if err := provider.SyncTurn(request); err != nil {
		t.Fatalf("duplicate sync: %v", err)
	}
	closeProvider(t, provider)
	candidates, memories, _ := store.counts()
	if candidates != 1 || memories != 1 {
		t.Fatalf("duplicate turn created duplicate state: candidates=%d memories=%d", candidates, memories)
	}
}

func TestProviderRejectsSyncWhenBoundedQueueIsFull(t *testing.T) {
	store := &recordingStore{}
	embedder := &blockingEmbedder{started: make(chan struct{}), release: make(chan struct{})}
	provider := newTestProvider(t, store, embedder, ProviderOptions{QueueSize: 1, JobTimeout: time.Second})
	request := func(id string) TurnSyncRequest {
		return TurnSyncRequest{RunID: "run_capacity", Message: domain.Message{
			ID: id, Role: "user", Content: "Remember that message " + id + " is durable.",
		}}
	}
	if err := provider.SyncTurn(request("one")); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	<-embedder.started
	if err := provider.SyncTurn(request("two")); err != nil {
		t.Fatalf("queued sync: %v", err)
	}
	if err := provider.SyncTurn(request("three")); !errors.Is(err, ErrSyncQueueFull) {
		t.Fatalf("full queue error=%v want=%v", err, ErrSyncQueueFull)
	}
	if !containsEventType(store.eventTypes(), domain.EventMemorySyncRejected) {
		t.Fatalf("queue rejection was not observable: %#v", store.eventTypes())
	}
	close(embedder.release)
	closeProvider(t, provider)
}

func TestProviderCloseReportsTimeoutAndCanFinishDrain(t *testing.T) {
	store := &recordingStore{}
	embedder := &blockingEmbedder{started: make(chan struct{}), release: make(chan struct{})}
	provider := newTestProvider(t, store, embedder, ProviderOptions{QueueSize: 1, JobTimeout: time.Second})
	if err := provider.SyncTurn(TurnSyncRequest{RunID: "run_close", Message: domain.Message{
		ID: "msg_close", Role: "user", Content: "Remember that shutdown drains accepted work.",
	}}); err != nil {
		t.Fatalf("sync: %v", err)
	}
	<-embedder.started
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	err := provider.Close(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close error=%v want deadline exceeded", err)
	}
	close(embedder.release)
	closeProvider(t, provider)
}

func TestMemoryProviderSyncFailureDoesNotChangeCompletedRun(t *testing.T) {
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
	provider := newTestProvider(t, fileStore, embedder, ProviderOptions{QueueSize: 4, JobTimeout: time.Second})
	if err := provider.SyncTurn(TurnSyncRequest{RunID: run.ID, Message: domain.Message{
		ID: "msg_failed", ConversationID: conversation.ID, Role: "user",
		Content: "Remember that AgentFlow uses Postgres for durable state.",
	}}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	<-embedder.started
	close(embedder.release)
	closeProvider(t, provider)

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

func newTestProvider(t *testing.T, store ProviderStore, embedder Embedder, options ProviderOptions) *BuiltinProvider {
	t.Helper()
	provider := NewBuiltinProvider(store, embedder, options)
	if err := provider.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize provider: %v", err)
	}
	return provider
}

func closeProvider(t *testing.T, provider *BuiltinProvider) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := provider.Close(ctx); err != nil {
		t.Fatalf("close provider: %v", err)
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

func containsEventType(items []domain.RunEventType, want domain.RunEventType) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
