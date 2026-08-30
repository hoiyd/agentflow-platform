package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/store"
)

func TestObservableStorePublishesCommittedEventWithAssignedSequence(t *testing.T) {
	base, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatal(err)
	}
	hub := event.NewHub(4)
	observed := newObservableStore(base, hub)
	conversation, err := observed.CreateConversation("observable store")
	if err != nil {
		t.Fatal(err)
	}
	run, err := observed.CreateRunWithContract("agent_planner", conversation.ID, domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}, Mode: "single",
		Agent: domain.RuntimeAgentSnapshot{ID: "agent_planner", Executor: domain.DefaultAgentExecutor},
		Model: domain.RuntimeModelSnapshot{Provider: "local", Model: "test"},
		ContextAssembly: domain.ContextAssemblyConfig{
			AssemblerVersion: "context-assembler-v1", ContextWindowTokens: 128000,
			OutputReserveTokens: 8192, SafetyMarginTokens: 4096, HistoryMaxTokens: 64000,
			MemoryMaxTokens: 8000, KnowledgeMaxTokens: 16000,
		},
	}, nil)

	if err != nil {
		t.Fatal(err)
	}
	subscription, err := hub.SnapshotAndSubscribe(context.Background(), run.ID, func() (domain.RunProjectionSnapshot, error) {
		replay, _, loadErr := observed.GetRunReplay(run.ID)
		return replay.Projection, loadErr
	})
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	created, err := observed.CreateRunEvent(domain.RunEvent{Type: domain.EventRunStarted, RunID: run.ID})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case delivered := <-subscription.Events:
		if delivered.ID != created.ID || delivered.Sequence != 1 {
			t.Fatalf("subscriber received pre-commit shape: %#v", delivered)
		}
	case <-time.After(time.Second):
		t.Fatal("committed event was not published")
	}

	scoped := observed.ForWorkspace(domain.NewWorkspaceScope(domain.DefaultWorkspaceID))
	workspaceCreated, err := scoped.CreateRunEvent(domain.RunEvent{Type: domain.EventRunCompleted, RunID: run.ID})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case delivered := <-subscription.Events:
		if delivered.ID != workspaceCreated.ID || delivered.Sequence != 2 {
			t.Fatalf("workspace subscriber received unexpected event: %#v", delivered)
		}
	case <-time.After(time.Second):
		t.Fatal("workspace-scoped committed event was not published")
	}
}

func TestObservableStoreDoesNotPublishFailedCommit(t *testing.T) {
	base, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatal(err)
	}
	hub := event.NewHub(1)
	observed := newObservableStore(base, hub)
	_, err = observed.CreateRunEvent(domain.RunEvent{Type: domain.RunEventType("unknown.event"), RunID: "run-1"})
	if err == nil {
		t.Fatal("invalid event commit should fail")
	}
}

func TestObservableStoreCloseDelegatesWhenSupported(t *testing.T) {
	base, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := newObservableStore(base, event.NewHub(1)).Close(); err != nil {
		t.Fatalf("store without Close should be a no-op: %v", err)
	}

	wantErr := errors.New("close failed")
	wrapped := &closingStore{Store: base, closeErr: wantErr}
	if err := newObservableStore(wrapped, event.NewHub(1)).Close(); !errors.Is(err, wantErr) {
		t.Fatalf("close error=%v want %v", err, wantErr)
	}
}

type closingStore struct {
	store.Store
	closeErr error
}

func (s *closingStore) Close() error { return s.closeErr }
