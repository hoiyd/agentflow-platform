package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"agentflow-platform/apps/api/app/runcommands"
	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/checkpoint"
	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/identity"
	"agentflow-platform/apps/api/internal/inference/openai"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/modelroute"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
	"agentflow-platform/apps/api/internal/tool"
)

// Only timing is instrumented: the real input/receipt/message/Run transaction
// commits before the callback issues a real Stop and Prepare attempts its CAS.
type inputStartupBoundary struct {
	agent.RuntimeInbox
	afterCommit func(domain.Run) error
}

func (b *inputStartupBoundary) CreateInputRun(ctx context.Context, id, owner string, stopped bool, agentID, conversation string, snapshot domain.RuntimeSnapshot, contract *domain.CompletionContract) (domain.Run, error) {
	run, err := b.RuntimeInbox.CreateInputRun(ctx, id, owner, stopped, agentID, conversation, snapshot, contract)
	if err == nil && b.afterCommit != nil {
		err = b.afterCommit(run)
	}
	return run, err
}

func TestDurableInputStartupCancellationPostgres(t *testing.T) {
	database := pgfixture.DatabaseURL(t)
	s, err := store.NewPostgresStore(database)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	workspace, err := s.DefaultWorkspace(t.Context(), identity.SuperUserID)
	if err != nil {
		t.Fatal(err)
	}
	owned, err := s.ForWorkspace(domain.NewWorkspaceScope(workspace)).CreateAgent(domain.Agent{Name: "Startup boundary", Executor: domain.DefaultAgentExecutor, Tools: []string{}, Skills: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "refused startup must not call provider", 500)
	}))
	defer provider.Close()
	client := openai.NewClient("fixture-only", provider.URL, "startup-fixture")
	routes, err := modelroute.Catalog(client, domain.ContextAssemblyConfig{}, domain.RuntimeRunBudget{})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := tool.NewManager("")
	if err != nil {
		t.Fatal(err)
	}
	inbox := &inputStartupBoundary{RuntimeInbox: s}
	runtime, err := agent.NewRuntime(agent.RuntimeOptions{Store: s, Inbox: inbox, EmbeddingClient: client, ModelRoutes: routes, Tools: manager, CheckpointProvider: checkpoint.NewInternalProvider(s)})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"single", "multi_agent", "autonomous"} {
		for _, scenario := range []string{"Stop before Start", "already canceled", "another Start won"} {
			t.Run(mode+"/"+scenario, func(t *testing.T) {
				conversation, err := s.CreateConversationInWorkspace(workspace, "Durable startup cancellation")
				if err != nil {
					t.Fatal(err)
				}
				parent, err := runtime.PrepareChatRunWithContract(t.Context(), owned.ID, conversation.ID, nil)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := runtime.CompleteRun(parent.Run.ID); err != nil {
					t.Fatal(err)
				}
				input := domain.RunInputRequest{Kind: "follow_up", RunID: parent.Run.ID, AgentID: owned.ID, Mode: mode, Content: "A separately queued task", IdempotencyKey: "startup-boundary"}
				receipt, err := s.EnqueueRunInput(t.Context(), identity.SuperUserID, workspace, conversation.ID, input)
				if err != nil {
					t.Fatal(err)
				}
				var successor domain.Run
				inbox.afterCommit = func(run domain.Run) error {
					successor = run
					if scenario == "another Start won" {
						_, err := s.TransitionRun(domain.RunTransition{RunID: run.ID, From: domain.RunRunning, To: domain.RunRunning, EventType: domain.EventRunStarted})
						return err
					}
					if _, err := runtime.CancelRun(run.ID); err != nil {
						return err
					}
					if scenario == "already canceled" {
						_, err := runtime.FailRun(run.ID, context.Canceled)
						return err
					}
					return nil
				}
				controller := concurrency.NewRunController(concurrency.RunOptions{MaxConcurrent: 1})
				commands := runcommands.Commands{Store: s, Runtime: runtime, Capacity: controller}
				err = commands.Start(t.Context(), runcommands.Scope{Owner: identity.SuperUserID, Workspace: domain.NewWorkspaceScope(workspace)}, domain.ChatRequest{ConversationID: conversation.ID, AgentID: owned.ID, Mode: mode, Message: input.Content}, nil, &runcommands.Followup{Input: receipt}, runcommands.Observer{})
				if !errors.Is(err, agent.ErrRunStateChanged) {
					t.Fatalf("startup must refuse stale execution: %v", err)
				}
				if successor.ID == "" {
					t.Fatal("durable input transaction was not exercised")
				}
				reopened, err := store.NewPostgresStore(database)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				current, found, err := reopened.GetRun(successor.ID)
				want := domain.RunCanceled
				if scenario == "another Start won" {
					want = domain.RunRunning
				}
				if err != nil || !found || current.Status != want {
					t.Errorf("startup refusal did not preserve/settle winner: status=%s want=%s found=%t err=%v", current.Status, want, found, err)
				}
				items, err := reopened.ListRunInputs(t.Context(), identity.SuperUserID, workspace, conversation.ID)
				if err != nil || len(items) != 1 || items[0].Status != "applied" || items[0].AppliedRunID != successor.ID {
					t.Fatalf("consumption identity changed: %+v %v", items, err)
				}
				messages, err := reopened.ListMessages(conversation.ID)
				if err != nil || len(messages) != 1 || messages[0].ID != receipt.ID {
					t.Errorf("input message duplicated/lost: %+v %v", messages, err)
				}
				runs, err := reopened.ListRuns()
				if err != nil {
					t.Fatal(err)
				}
				count := 0
				for _, run := range runs {
					if run.ConversationID == conversation.ID {
						count++
					}
				}
				if count != 2 {
					t.Errorf("startup created additional successor: %d", count)
				}
				events, err := reopened.ListRunEvents(successor.ID)
				if err != nil {
					t.Fatal(err)
				}
				wantEvents := []domain.RunEventType{domain.EventRunCreated, domain.EventRunCancelRequested, domain.EventRunCanceled}
				if scenario == "another Start won" {
					wantEvents = []domain.RunEventType{domain.EventRunCreated, domain.EventRunStarted}
				}
				if len(events) != len(wantEvents) {
					t.Errorf("unexpected startup facts: %+v", events)
				}
				for i, item := range events {
					if i < len(wantEvents) && (item.Type != wantEvents[i] || item.Sequence != int64(i+1)) {
						t.Errorf("wrong lifecycle pairing at %d: %+v", i, item)
					}
					if item.StageID != "" || item.TurnID != "" {
						t.Errorf("invented worker identity: %+v", item)
					}
				}
				if failures := event.CheckRuntimeInvariants(current, events); len(failures) != 0 {
					t.Errorf("startup/cancel lifecycle orphaned: %+v", failures)
				}
				replay, found, err := reopened.GetRunReplay(successor.ID)
				if err != nil || !found || replay.Projection.Run.Status != want {
					t.Errorf("Replay disagrees with startup: found=%t err=%v", found, err)
				}
				if calls.Load() != 0 {
					t.Errorf("refused startup made %d provider calls", calls.Load())
				}
				reservation, err := controller.Reserve()
				if err != nil {
					t.Fatalf("startup refusal leaked admission: %v", err)
				}
				reservation.Cancel()
				data, _ := json.Marshal(map[string]any{"schema": "ref01-startup-cancellation-v1", "mode": mode, "scenario": scenario, "source_run_id": parent.Run.ID, "input": input, "receipt": items[0], "successor": current, "events": events, "provider_calls": calls.Load(), "limitations": []string{"production Commands/Runtime/Postgres composition, no HTTP transport", "timing callback after real input transaction only", "no model/provider behavior measured"}})
				t.Log("REF01_STARTUP_EVIDENCE " + string(data))
			})
		}
	}
}
