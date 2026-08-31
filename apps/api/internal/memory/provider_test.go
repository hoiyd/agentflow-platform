package memory

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/store"
)

type embeddingStub struct {
	embedding openai.Embedding
	err       error
}

type retryEmbeddingStub struct {
	attempts int
}

func (e *retryEmbeddingStub) EmbedText(context.Context, string) (openai.Embedding, error) {
	e.attempts++
	if e.attempts == 1 {
		return openai.Embedding{}, errors.New("temporary embedding failure")
	}
	return openai.Embedding{Vector: []float64{1}, Provider: "test", Model: "retry"}, nil
}

func (e embeddingStub) EmbedText(context.Context, string) (openai.Embedding, error) {
	return e.embedding, e.err
}

func TestBuiltinProviderCommitsAndRecallsMemory(t *testing.T) {
	fileStore, err := store.NewFileStore(filepath.Join(t.TempDir(), "agentflow.json"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	provider := newTestProvider(t, fileStore, embeddingStub{embedding: openai.Embedding{
		Vector: []float64{1, 0, 0}, Provider: "test", Model: "embedding-v1", Dimensions: 3,
	}}, ProviderOptions{})
	defer closeProvider(t, provider)

	created, err := provider.Commit(context.Background(), domain.Memory{Kind: " note ", Content: " durable fact "})
	if err != nil {
		t.Fatalf("create memory: %v", err)
	}
	if created.Kind != "note" || created.Content != "durable fact" {
		t.Fatalf("memory was not normalized: %#v", created)
	}

	items, err := provider.Recall(context.Background(), domain.MemorySearch{Query: " durable fact ", Limit: 1})
	if err != nil {
		t.Fatalf("search memory: %v", err)
	}
	if len(items) != 1 || items[0].Memory.ID != created.ID {
		t.Fatalf("unexpected search result: %#v", items)
	}
}

func TestBuiltinProviderClassifiesEmbeddingFailure(t *testing.T) {
	want := errors.New("embedding provider unavailable")
	fileStore, err := store.NewFileStore(filepath.Join(t.TempDir(), "agentflow.json"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	provider := newTestProvider(t, fileStore, embeddingStub{err: want}, ProviderOptions{MaxAttempts: 1})
	defer closeProvider(t, provider)

	_, err = provider.Recall(context.Background(), domain.MemorySearch{Query: "fact"})
	if !IsEmbeddingError(err) || !errors.Is(err, want) {
		t.Fatalf("expected typed embedding error, got %v", err)
	}
}

func TestBuiltinProviderRequiresInitialization(t *testing.T) {
	provider := NewBuiltinProvider(&recordingStore{}, immediateEmbedder{}, ProviderOptions{})
	if _, err := provider.Recall(context.Background(), domain.MemorySearch{Query: "fact"}); !errors.Is(err, ErrProviderNotInitialized) {
		t.Fatalf("recall error=%v want=%v", err, ErrProviderNotInitialized)
	}
	if err := provider.SyncTurn(TurnSyncRequest{Message: domain.Message{Role: "user", Content: "Remember this."}}); !errors.Is(err, ErrProviderNotInitialized) {
		t.Fatalf("sync error=%v want=%v", err, ErrProviderNotInitialized)
	}
}

func TestBuiltinProviderRetriesTransientOperationsWithinBound(t *testing.T) {
	embedder := &retryEmbeddingStub{}
	provider := newTestProvider(t, &recordingStore{}, embedder, ProviderOptions{
		MaxAttempts: 2, RetryBaseDelay: time.Millisecond,
	})
	defer closeProvider(t, provider)
	if _, err := provider.Commit(context.Background(), domain.Memory{Kind: "fact", Content: "retry safely"}); err != nil {
		t.Fatalf("commit after retry: %v", err)
	}
	if embedder.attempts != 2 {
		t.Fatalf("embedding attempts=%d want=2", embedder.attempts)
	}
}

func TestBuiltinProviderLifecycleAndExplicitProposal(t *testing.T) {
	store := &recordingStore{}
	provider := NewBuiltinProvider(store, immediateEmbedder{}, ProviderOptions{})
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := provider.Initialize(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled initialize error=%v", err)
	}
	if err := provider.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if err := provider.Initialize(context.Background()); err != nil {
		t.Fatalf("repeated initialize: %v", err)
	}
	if _, err := provider.Recall(context.Background(), domain.MemorySearch{}); err == nil {
		t.Fatal("empty recall query should fail")
	}
	if _, err := provider.Commit(context.Background(), domain.Memory{Content: "missing kind"}); err == nil {
		t.Fatal("missing memory kind should fail")
	}
	if _, err := provider.Commit(context.Background(), domain.Memory{Kind: "fact"}); err == nil {
		t.Fatal("missing memory content should fail")
	}
	proposal, err := provider.Propose(context.Background(), ProposalRequest{
		RunID: "run_propose", Message: domain.Message{ID: "msg_propose", Role: "user", Content: "Remember that proposals are separate from commits."},
	})
	if err != nil || !proposal.Proposed || !proposal.Accepted || proposal.Duplicate {
		t.Fatalf("explicit proposal=%#v err=%v", proposal, err)
	}
	_, memories, _ := store.counts()
	if memories != 0 {
		t.Fatalf("proposal committed memory directly: %d", memories)
	}
	closeProvider(t, provider)
	closeProvider(t, provider)
	if err := provider.SyncTurn(TurnSyncRequest{Message: domain.Message{Role: "user", Content: "Remember this."}}); !errors.Is(err, ErrProviderClosed) {
		t.Fatalf("closed sync error=%v", err)
	}
	if _, err := provider.Recall(context.Background(), domain.MemorySearch{Query: "fact"}); !errors.Is(err, ErrProviderClosed) {
		t.Fatalf("closed recall error=%v", err)
	}
	if err := provider.Initialize(context.Background()); !errors.Is(err, ErrProviderClosed) {
		t.Fatalf("closed initialize error=%v", err)
	}
}

func TestBuiltinProviderCanCloseBeforeInitialization(t *testing.T) {
	provider := NewBuiltinProvider(&recordingStore{}, immediateEmbedder{}, ProviderOptions{})
	closeProvider(t, provider)
	if err := provider.Initialize(context.Background()); !errors.Is(err, ErrProviderClosed) {
		t.Fatalf("initialize after close error=%v", err)
	}
	var nilProvider *BuiltinProvider
	if err := nilProvider.Close(context.Background()); err != nil {
		t.Fatalf("nil provider close: %v", err)
	}
}

func TestProviderRetryClassification(t *testing.T) {
	if providerRetryable(context.Canceled) {
		t.Fatal("canceled operation must not retry")
	}
	if !providerRetryable(errors.New("temporary unclassified failure")) {
		t.Fatal("unclassified dependency failure should retry within the provider bound")
	}
	if err := newOperationError("noop", 1, nil); err != nil {
		t.Fatalf("nil operation error=%v", err)
	}
}
