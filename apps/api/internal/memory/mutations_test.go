package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/modelprovider"
	"agentflow-platform/apps/api/internal/store"
)

type mutationEmbedderFunc func(context.Context, string) (modelprovider.Embedding, error)

func (f mutationEmbedderFunc) EmbedText(ctx context.Context, text string) (modelprovider.Embedding, error) {
	return f(ctx, text)
}

func TestMemoryAdministrationLifecycleAndFailures(t *testing.T) {
	s, err := store.NewFileStore(t.TempDir() + "/memory.json")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p := NewBuiltinProvider(s, immediateEmbedder{}, ProviderOptions{MaxAttempts: 1})
	if _, err := p.GetMemory(ctx, "", "missing"); !errors.Is(err, ErrProviderNotInitialized) {
		t.Fatal(err)
	}
	if _, err := p.MutateMemory(ctx, "", "missing", domain.MemoryMutation{}); !errors.Is(err, ErrProviderNotInitialized) {
		t.Fatal(err)
	}
	if err := p.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	defer closeProvider(t, p)
	m, err := p.Commit(ctx, domain.Memory{Kind: "fact", Content: "old"})
	if err != nil {
		t.Fatal(err)
	}
	cmd := domain.MemoryMutation{OperationID: "op", ExpectedVersion: 1, Action: "replace", Content: "corrected", Actor: "user", Reason: "wrong fact"}
	if _, err := p.MutateMemory(ctx, m.WorkspaceID, m.ID, domain.MemoryMutation{}); !errors.Is(err, store.ErrMemoryMutationInvalid) {
		t.Fatal(err)
	}
	if _, err := p.MutateMemory(ctx, "other", m.ID, cmd); !errors.Is(err, store.ErrMemoryMissing) {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := p.GetMemory(canceled, "", m.ID); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := p.MutateMemory(canceled, "", m.ID, cmd); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	p.embedder = mutationEmbedderFunc(func(context.Context, string) (modelprovider.Embedding, error) {
		return modelprovider.Embedding{}, errors.New("embedding offline")
	})
	if _, err := p.MutateMemory(ctx, "", m.ID, cmd); !IsEmbeddingError(err) {
		t.Fatalf("embedding error: %v", err)
	}
	detail, _ := p.GetMemory(ctx, "", m.ID)
	if detail.Memory.Version != 1 || len(detail.Changes) != 0 {
		t.Fatal("embedding failure persisted mutation")
	}
	p.embedder = immediateEmbedder{}
	r, err := p.MutateMemory(ctx, "", m.ID, cmd)
	if err != nil || !r.Applied {
		t.Fatalf("correction: %+v %v", r, err)
	}
	p.embedder = mutationEmbedderFunc(func(context.Context, string) (modelprovider.Embedding, error) {
		t.Error("duplicate/deletion must not embed")
		return modelprovider.Embedding{}, errors.New("offline")
	})
	r, err = p.MutateMemory(ctx, "", m.ID, cmd)
	if err != nil || r.Applied {
		t.Fatalf("duplicate: %+v %v", r, err)
	}
	stale := cmd
	stale.OperationID = "stale"
	if _, err := p.MutateMemory(ctx, "", m.ID, stale); !errors.Is(err, store.ErrMemoryConflict) {
		t.Fatal(err)
	}
	del := domain.MemoryMutation{OperationID: "delete", ExpectedVersion: 2, Action: "delete", Actor: "user", Reason: "withdraw"}
	if _, err := p.MutateMemory(ctx, "", m.ID, del); err != nil {
		t.Fatal(err)
	}
	cmd.OperationID = "resurrect"
	cmd.ExpectedVersion = 3
	if _, err := p.MutateMemory(ctx, "", m.ID, cmd); !errors.Is(err, store.ErrMemoryConflict) {
		t.Fatal(err)
	}
	unsupported := newTestProvider(t, &recordingStore{}, immediateEmbedder{}, ProviderOptions{})
	defer closeProvider(t, unsupported)
	if _, err := unsupported.GetMemory(ctx, "", "id"); err == nil {
		t.Fatal("missing administration capability")
	}
}

func TestMemoryMutationRechecksAfterEmbedding(t *testing.T) {
	for _, mode := range []string{"cancel", "concurrent-delete"} {
		t.Run(mode, func(t *testing.T) {
			s, err := store.NewFileStore(t.TempDir() + "/memory.json")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			p := newTestProvider(t, s, immediateEmbedder{}, ProviderOptions{MaxAttempts: 1})
			defer closeProvider(t, p)
			m, err := p.Commit(ctx, domain.Memory{Kind: "fact", Content: "old"})
			if err != nil {
				t.Fatal(err)
			}
			p.embedder = mutationEmbedderFunc(func(context.Context, string) (modelprovider.Embedding, error) {
				if mode == "cancel" {
					cancel()
				} else {
					_, err := s.MutateMemory("", m.ID, domain.MemoryMutation{OperationID: "delete", Action: "delete", ExpectedVersion: 1, Actor: "other", Reason: "withdraw"}, domain.MemoryEmbedding{})
					if err != nil {
						t.Fatal(err)
					}
				}
				return modelprovider.Embedding{Vector: []float64{1}}, nil
			})
			_, err = p.MutateMemory(ctx, "", m.ID, domain.MemoryMutation{OperationID: "correction", Action: "replace", Content: "new", ExpectedVersion: 1, Actor: "user", Reason: "correction"})
			want := error(store.ErrMemoryConflict)
			if mode == "cancel" {
				want = context.Canceled
			}
			if !errors.Is(err, want) {
				t.Fatalf("post embedding error=%v", err)
			}
		})
	}
}

type failingMutationStore struct {
	*store.FileStore
	fail string
}

func (s failingMutationStore) FindMemoryChange(w, o string) (*domain.MemoryChange, error) {
	if s.fail == "find" {
		return nil, errors.New("find failed")
	}
	return s.FileStore.FindMemoryChange(w, o)
}
func (s failingMutationStore) GetMemoryDetail(w, id string) (domain.MemoryDetail, error) {
	if s.fail == "get" {
		return domain.MemoryDetail{}, errors.New("get failed")
	}
	return s.FileStore.GetMemoryDetail(w, id)
}
func (s failingMutationStore) MutateMemory(w, id string, c domain.MemoryMutation, e domain.MemoryEmbedding) (domain.MemoryMutationResult, error) {
	return domain.MemoryMutationResult{}, errors.New("persist failed")
}

func TestMemoryAdministrationStoreErrors(t *testing.T) {
	for _, mode := range []string{"find", "get", "persist"} {
		t.Run(mode, func(t *testing.T) {
			s, err := store.NewFileStore(t.TempDir() + "/memory.json")
			if err != nil {
				t.Fatal(err)
			}
			p := newTestProvider(t, failingMutationStore{FileStore: s, fail: mode}, immediateEmbedder{}, ProviderOptions{MaxAttempts: 1})
			defer closeProvider(t, p)
			m, err := p.Commit(context.Background(), domain.Memory{Kind: "fact", Content: "old"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := p.MutateMemory(context.Background(), "", m.ID, domain.MemoryMutation{OperationID: "op", Action: "replace", ExpectedVersion: 1, Content: "new", Actor: "user", Reason: "correction"}); err == nil {
				t.Fatal("ignored store failure")
			}
			detail, _ := s.GetMemoryDetail("", m.ID)
			if detail.Memory.Version != 1 {
				t.Fatal("partial update")
			}
		})
	}
}

func TestLateMemorySyncCannotRestoreWithdrawnSource(t *testing.T) {
	s, err := store.NewFileStore(t.TempDir() + "/memory.json")
	if err != nil {
		t.Fatal(err)
	}
	conv, err := s.CreateConversationInWorkspace("team", "late sync")
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateRunWithContract("agent_planner", conv.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := newTestProvider(t, s, immediateEmbedder{}, ProviderOptions{MaxAttempts: 1})
	defer closeProvider(t, p)
	msg := domain.Message{ID: "source", WorkspaceID: conv.WorkspaceID, ConversationID: conv.ID, Role: "user", Content: "Remember that the release day is Monday.", CreatedAt: time.Now()}
	proposal, err := p.Propose(context.Background(), ProposalRequest{RunID: run.ID, IdempotencyKey: "first", Message: msg})
	if err != nil || !proposal.Accepted || proposal.Candidate.WorkspaceID != conv.WorkspaceID {
		t.Fatalf("proposal: %+v %v", proposal, err)
	}
	m, err := p.Commit(context.Background(), domain.Memory{WorkspaceID: conv.WorkspaceID, Kind: "fact", Content: "Monday", SourceMessageID: msg.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.MutateMemory(context.Background(), conv.WorkspaceID, m.ID, domain.MemoryMutation{OperationID: "withdraw", Action: "delete", ExpectedVersion: 1, Actor: "user", Reason: "wrong"})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the worker holding an accepted candidate when the user deletes its source.
	p.commitCandidate(context.Background(), TurnSyncRequest{RunID: run.ID, IdempotencyKey: "first", Message: msg}, proposal.Candidate)
	late, err := p.Propose(context.Background(), ProposalRequest{RunID: run.ID, IdempotencyKey: "late", Message: msg})
	if err != nil || late.Accepted || late.Candidate.PolicyReason != "source_memory_mutated" {
		t.Fatalf("late accepted: %+v %v", late, err)
	}
	items, err := p.Recall(context.Background(), domain.MemorySearch{WorkspaceID: conv.WorkspaceID, Query: "Monday"})
	if err != nil || len(items) != 0 {
		t.Fatalf("resurrected: %+v %v", items, err)
	}
	events, err := s.ListRunEvents(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.Type == domain.EventMemorySyncFailed {
			found = true
		}
		if e.Type == domain.EventMemorySyncCompleted {
			t.Fatal("failed sync reported completion")
		}
	}
	if !found {
		t.Fatal("missing sync failure evidence")
	}
}
