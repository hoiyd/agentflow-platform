package httpapi

import (
	"context"
	"errors"
	"testing"

	"agentflow-platform/apps/api/app/runcommands"
	"agentflow-platform/apps/api/internal/agent"
	"agentflow-platform/apps/api/internal/concurrency"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
)

// Exercise the non-HTTP entry with the real Runtime/Store: observer disconnect
// must not drop request metadata, completion, memory sync or writer release.
func TestRunCommandDetachesOnlyAfterAdmission(t *testing.T) {
	storage := fixturestore.New()
	controller := concurrency.NewRunController(concurrency.RunOptions{MaxConcurrent: 1})
	runtime := newRuntimeForTest(agent.RuntimeOptions{Store: storage}, newLocalFallbackOpenAIClientForTest())
	type key struct{}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "request-metadata"))
	defer cancel()
	probe := preparationContextProbe{AgentRuntimeOperations: runtime, check: func(ctx context.Context) {
		if ctx.Err() != nil || ctx.Value(key{}) != "request-metadata" {
			t.Fatalf("admitted context lost lifetime/metadata: %v", ctx.Err())
		}
	}}
	done, released := false, false
	memory := &recordingMemoryOperations{onSync: func() {
		if !done {
			t.Fatal("memory sync preceded terminal output")
		}
	}}
	commands := runcommands.Commands{Store: storage, Runtime: probe, Capacity: controller, Memory: memory,
		AfterRelease: func(ctx context.Context, scope runcommands.Scope, conversation string) {
			if !done || ctx.Err() != nil || scope.Owner != "super" {
				t.Fatal("invalid release boundary")
			}
			reservation, err := controller.Reserve()
			if err != nil {
				t.Fatalf("capacity leaked: %v", err)
			}
			nextRelease, err := reservation.Start(ctx, conversation)
			if err != nil {
				t.Fatalf("writer leaked: %v", err)
			}
			nextRelease()
			released = true
		},
	}
	err := commands.Start(ctx, runcommands.Scope{Workspace: domain.NewWorkspaceScope(""), Owner: "super"}, domain.ChatRequest{Message: "Summarize the execution status."}, nil, nil, runcommands.Observer{
		Conversation: func(string) { cancel() },
		Done: func(chunk domain.ChatChunk) {
			if chunk.Status != "completed" {
				t.Fatalf("unexpected terminal output: %+v", chunk)
			}
			done = true
		},
	})
	if err != nil || !done || !released || len(memory.jobs) != 1 {
		t.Fatalf("incomplete admitted execution: err=%v done=%v released=%v memory=%d", err, done, released, len(memory.jobs))
	}
	conversations, _ := storage.ListConversations()
	messages, err := storage.ListMessages(conversations[0].ID)
	if err != nil || len(messages) != 2 || messages[1].Role != "assistant" {
		t.Fatalf("answer not durable: %+v %v", messages, err)
	}
}

type preparationContextProbe struct {
	AgentRuntimeOperations
	check func(context.Context)
}

func (p preparationContextProbe) PrepareChatRunWithContract(ctx context.Context, agentID, conversation string, contract *domain.CompletionContract) (agent.PreparedRun, error) {
	p.check(ctx)
	return p.AgentRuntimeOperations.PrepareChatRunWithContract(ctx, agentID, conversation, contract)
}

// Headless callers must get the same pre-admission safeguards as HTTP, without
// transport-side credential checks or an observer. Rejection must not mutate state.
func TestHeadlessRecoveryRejectsWithoutWriting(t *testing.T) {
	for _, name := range []string{"owner", "resume credential", "continue credential", "missing", "lookup failure", "waiting input", "queued cancellation", "changed after admission"} {
		t.Run(name, func(t *testing.T) {
			backend, workspace, storage := newBoundaryHTTPStore(t)
			conversation, _ := storage.CreateConversation("headless refusal")
			run, err := storage.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = storage.UpdateRunStatus(run.ID, domain.RunWaitingForUser, "")
			if err != nil {
				t.Fatal(err)
			}
			controller := concurrency.NewRunController(concurrency.RunOptions{MaxConcurrent: 1, QueueSize: 1})
			commands := runcommands.Commands{Store: backend, Runtime: newRuntimeForTest(agent.RuntimeOptions{Store: storage}, nil), Capacity: controller}
			scope := runcommands.Scope{Workspace: domain.NewWorkspaceScope(""), Owner: "super"}
			ctx := context.Background()
			id, input := run.ID, "resume"
			reason := runcommands.Invalid
			observer := runcommands.Observer{}
			wantStatus := domain.RunWaitingForUser
			switch name {
			case "owner":
				scope.Owner = ""
			case "resume credential", "continue credential":
				input = "Bearer headless-secret"
			case "missing":
				id = "missing"
				reason = runcommands.Missing
			case "lookup failure":
				workspace.getRunErr = errors.New("lookup unavailable")
				reason = ""
			case "waiting input":
				input = ""
			case "queued cancellation":
				reservation, err := controller.Reserve()
				if err != nil {
					t.Fatal(err)
				}
				release, err := reservation.Start(ctx, conversation.ID)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			case "changed after admission":
				reason = runcommands.Conflict
				wantStatus = domain.RunCompleted
				observer.Run = func(domain.Run) {
					if _, err := storage.UpdateRunStatus(run.ID, wantStatus, ""); err != nil {
						t.Fatal(err)
					}
				}
			}
			if name == "continue credential" {
				err = commands.Continue(ctx, scope, id, input, domain.AgentRoutingRequirements{}, runcommands.Observer{})
			} else {
				err = commands.Resume(ctx, scope, id, input, observer)
			}
			if err == nil {
				t.Fatal("invalid command accepted")
			}
			if name == "queued cancellation" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation lost: %v", err)
				}
			} else if reason != "" {
				var refusal *runcommands.Error
				if !errors.As(err, &refusal) || refusal.Reason != reason {
					t.Fatalf("wrong refusal: %v", err)
				}
			}
			current, found, err := storage.GetRun(run.ID)
			if err != nil || !found || current.Status != wantStatus {
				t.Fatalf("rejection changed Run: %+v %v", current, err)
			}
			messages, err := storage.ListMessages(conversation.ID)
			if err != nil || len(messages) != 0 {
				t.Fatalf("rejection wrote messages: %+v %v", messages, err)
			}
			reservation, err := controller.Reserve()
			if err != nil {
				t.Fatalf("capacity leaked: %v", err)
			}
			reservation.Cancel()
		})
	}
}
