package app

import (
	"encoding/json"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/clarification"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// Exercise atomic writes through the production publication wrapper and real
// Postgres: missing one sequence slot otherwise stalls every later live event.
func TestClarificationCommittedEventsReachHub(t *testing.T) {
	base, err := store.NewPostgresStore(pgfixture.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	hub := event.NewHub(32)
	observed := newObservableStore(base, hub)
	c, err := base.CreateConversation("durable event publication")
	if err != nil {
		t.Fatal(err)
	}
	agent, _, _ := base.GetDefaultAgent()
	run, err := base.CreateRunWithContract(agent.ID, c.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, Mode: "single", Agent: domain.RuntimeAgentSnapshot{ID: agent.ID, Executor: domain.DefaultAgentExecutor}, RunBudget: &domain.RuntimeRunBudget{}, Embedding: domain.RuntimeEmbeddingSnapshot{Provider: "local", Model: "fixture", BaseURL: "http://localhost:11434/api/embed", Dimensions: 1}, ContextAssembly: domain.ContextAssemblyConfig{AssemblerVersion: "context-assembler-v1", ContextWindowTokens: 128000, OutputReserveTokens: 8192, SafetyMarginTokens: 4096, HistoryMaxTokens: 64000, MemoryMaxTokens: 8000, KnowledgeMaxTokens: 16000}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = base.UpdateRunStatus(run.ID, domain.RunRunning, ""); err != nil {
		t.Fatal(err)
	}
	if _, err = observed.CreateRunEvent(domain.RunEvent{RunID: run.ID, TurnID: "real-turn", Type: domain.EventTurnStarted, Payload: map[string]any{"agent_id": agent.ID}}); err != nil {
		t.Fatal(err)
	}
	subscription, err := hub.SnapshotAndSubscribe(t.Context(), run.ID, func() (domain.RunProjectionSnapshot, error) {
		read, _, err := base.GetRunReplay(run.ID)
		return read.Projection, err
	})
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()
	exported := []domain.RunEvent{}
	observed.observe = func(e domain.RunEvent) { exported = append(exported, e) }
	target := observableClarifications{Store: base, publish: observed.publishCommitted}
	pause, err := target.SuspendClarification(t.Context(), domain.Clarification{RunID: run.ID, ConversationID: c.ID, TurnID: "real-turn", Questions: []domain.ClarificationQuestion{{ID: "goal", Question: "What outcome?"}}}, json.RawMessage(`{"version":1}`))
	if err != nil {
		t.Fatal(err)
	}
	sequence := subscription.Snapshot.AsOfSequence
	assertEvents := func(m clarification.Mutation) {
		t.Helper()
		for _, want := range m.Events {
			select {
			case got := <-subscription.Events:
				sequence++
				if got.Sequence != sequence || got.ID != want.ID {
					t.Fatalf("sequence gap: %+v", got)
				}
			case <-time.After(time.Second):
				t.Fatal("committed event lost")
			}
		}
	}
	assertEvents(pause)
	input := domain.ClarificationAnswer{Version: pause.Version, IdempotencyKey: "one", Answers: map[string]string{"goal": "A report"}}
	answered, err := target.AnswerClarification(t.Context(), identity.SuperUserID, c.WorkspaceID, run.ID, pause.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	assertEvents(answered)
	duplicate, err := target.AnswerClarification(t.Context(), identity.SuperUserID, c.WorkspaceID, run.ID, pause.ID, input)
	if err != nil || len(duplicate.Events) != 0 {
		t.Fatalf("duplicate published: %+v %v", duplicate, err)
	}
	input.Answers["goal"] = "Different"
	if _, err = target.AnswerClarification(t.Context(), identity.SuperUserID, c.WorkspaceID, run.ID, pause.ID, input); err == nil {
		t.Fatal("conflict accepted")
	}
	claimed, err := target.ClaimClarification(t.Context(), identity.SuperUserID, c.WorkspaceID, run.ID, pause.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertEvents(claimed)
	if len(exported) != len(pause.Events)+len(answered.Events)+len(claimed.Events) {
		t.Fatal("failed/duplicate mutation exported")
	}
	select {
	case item := <-subscription.Events:
		t.Fatalf("unexpected duplicate: %+v", item)
	default:
	}
}
