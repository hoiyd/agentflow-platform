package toolloop_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/taskstate"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
)

const taskPatch = `{"expected_version":0,"operations":[{"type":"upsert_task","task":{"id":"task-1","title":"Check evidence","details":"Retain exact facts","status":"pending"}}]}`

func taskStateFixture(t *testing.T, f *loopFixture, stageID string) *taskstate.Service {
	t.Helper()
	f.ctx = eventpkg.WithScope(f.ctx, eventpkg.Scope{RunID: f.run.ID, ConversationID: f.run.ConversationID, StageID: stageID, TurnID: "turn-first"})
	f.request.Trace.StepID = stageID
	service := taskstate.NewService(f.store, eventpkg.StoreSink{Store: f.store})
	catalog, err := tool.NewCatalog(service.ToolBinding())
	if err != nil {
		t.Fatal(err)
	}
	f.request.Catalog = catalog
	f.ctx = contextassembly.WithSession(f.ctx, contextassembly.Session{
		Config: contextassembly.DefaultConfig(), Sink: eventpkg.StoreSink{Store: f.store}, CurrentInput: f.request.Latest,
		LoadTaskState: func() (domain.TaskState, bool, error) { return service.Get(f.run.ConversationID) },
	})
	return service
}

func TestMultiRoundTaskStateWriteUsesRealStageOrTurnIdentity(t *testing.T) {
	for _, stageID := range []string{"", "stage-fixed"} {
		t.Run("stage="+stageID, func(t *testing.T) {
			f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 5, MaxToolCalls: 8}, func(round int, input wireRequest) any {
				switch round {
				case 1:
					return call(taskstate.UpdateToolName, `{"expected_version":0,"operations":[{"type":"set_goal","goal":"Check evidence"},{"type":"upsert_task","details":"wrong level"}]}`)
				case 2:
					if !strings.Contains(input.Messages[len(input.Messages)-1].Content, "/operations/1/details") {
						t.Error("schema failure was not returned for model correction")
					}
					return map[string]any{"choices": []any{map[string]any{"finish_reason": "tool_calls", "message": map[string]any{
						"role": "assistant", "tool_calls": []any{
							map[string]any{"id": "first", "type": "function", "function": map[string]any{"name": taskstate.UpdateToolName, "arguments": taskPatch}},
							map[string]any{"id": "second", "type": "function", "function": map[string]any{"name": taskstate.UpdateToolName, "arguments": `{"expected_version":1,"operations":[{"type":"set_goal","goal":"Finish evidence review"}]}`}},
						},
					}}}}
				default:
					found := false
					for _, message := range input.Messages {
						if strings.Contains(message.Content, "<task_state ") {
							if !strings.Contains(message.Content, `"version":2`) {
								t.Error("next request did not reload updated Task State")
							}
							found = true
						}
					}
					if !found {
						t.Error("Task State missing from follow-up context")
					}
					return answer("review complete")
				}
			})
			service := taskStateFixture(t, f, stageID)
			output, err := f.execute()
			if err != nil || output != "review complete" || f.requests.Load() != 3 {
				t.Fatalf("output=%q requests=%d err=%v", output, f.requests.Load(), err)
			}
			state, ok, err := service.Get(f.run.ConversationID)
			if err != nil || !ok || state.Version != 2 || len(state.Tasks) != 1 || state.Tasks[0].Details != "Retain exact facts" {
				t.Fatalf("state=%#v ok=%v err=%v", state, ok, err)
			}
			effects, err := f.store.ListToolEffects(f.run.ID)
			if err != nil || len(effects) != 2 {
				t.Fatalf("effects=%#v err=%v", effects, err)
			}
			for _, effect := range effects {
				if effect.StageID != stageID || effect.TurnID != "turn-first" || effect.Status != domain.ToolEffectCommitted {
					t.Fatalf("effect lost actual execution identity: %#v", effect)
				}
			}
			executor := tool.NewExecutor(f.request.Catalog, tool.ExecutorOptions{EffectJournal: f.store,
				Tracer: eventpkg.NewToolExecutionTracer(f.request.Trace.Recorder, f.run.ID, stageID)})
			request := tool.ExecutionRequest{RunID: f.run.ID, StageID: stageID, TurnID: "turn-first", CallID: effects[0].ToolCallID, Tool: taskstate.UpdateToolName, Arguments: json.RawMessage(taskPatch)}
			result := executor.Execute(f.ctx, request)
			if result.Error != nil || !result.Replayed {
				t.Fatalf("committed call did not replay: %#v", result)
			}
			if revisions, err := service.ListRevisions(f.run.ConversationID); err != nil || len(revisions) != 2 {
				t.Fatalf("replay appended a revision: count=%d err=%v", len(revisions), err)
			}
			request.CallID = "different-stale-call"
			if result = executor.Execute(f.ctx, request); result.Error == nil || !strings.Contains(result.Error.Message, "version conflict") {
				t.Fatalf("stale version was accepted: %#v", result)
			}
			if stageID == "" {
				request.CallID, request.TurnID = effects[0].ToolCallID, "turn-next"
				if result = executor.Execute(f.ctx, request); result.Replayed || result.Error == nil {
					t.Fatalf("different Turn reused a prior Turn's receipt: %#v", result)
				}
			}
		})
	}
}

func TestMultiRoundTaskStateUncertainWriteStopsTurn(t *testing.T) {
	f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 4}, func(int, wireRequest) any { return call(taskstate.UpdateToolName, taskPatch) })
	service := taskStateFixture(t, f, "")
	binding := service.ToolBinding()
	apply := binding.Handler
	binding.Handler = func(ctx context.Context, arguments json.RawMessage) (any, error) {
		if _, err := apply(ctx, arguments); err != nil {
			return nil, err
		}
		return nil, errors.New("write outcome unavailable")
	}
	var err error
	f.request.Catalog, err = tool.NewCatalog(binding)
	if err != nil {
		t.Fatal(err)
	}
	output, err := f.execute()
	state, _, _ := service.Get(f.run.ConversationID)
	effects, _ := f.store.ListToolEffects(f.run.ID)
	if err == nil || output != "" || f.requests.Load() != 1 || state.Version != 1 || len(effects) != 1 || effects[0].Status != domain.ToolEffectNeedsReconciliation {
		t.Fatalf("uncertain internal write continued: output=%q requests=%d version=%d effects=%#v err=%v", output, f.requests.Load(), state.Version, effects, err)
	}
}

func TestMultiRoundExternalWriteStillRequiresStage(t *testing.T) {
	var writes atomic.Int32
	f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 4}, func(int, wireRequest) any { return call("external_write", `{}`) })
	f.ctx = eventpkg.WithScope(f.ctx, eventpkg.Scope{RunID: f.run.ID, ConversationID: f.run.ConversationID, TurnID: "turn-first"})
	f.request.Trace.StepID = ""
	capability := policy.Capability{SideEffect: policy.SideEffectExternalWrite}
	catalog, err := tool.NewCatalogWithPolicy(policy.Policy{Version: "fixture", DefaultAction: policy.ActionAllow,
		Rules: []policy.Rule{{ID: "external-test", Tool: "external_write", Action: policy.ActionAllow, Capability: capability}}}, tool.Binding{
		Descriptor: tool.Descriptor{Name: "external_write", Parameters: tool.ObjectSchema(nil, nil), SideEffect: tool.SideEffectPolicy{Mode: tool.SideEffectExternal}, Security: capability},
		Handler:    func(context.Context, json.RawMessage) (any, error) { writes.Add(1); return "written", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	f.request.Catalog = catalog
	output, err := f.execute()
	if failure.Describe(err).Code != string(tool.ErrorIdempotencyRequired) || output != "" || writes.Load() != 0 || f.requests.Load() != 1 {
		t.Fatalf("unguarded external write: output=%q writes=%d requests=%d err=%v", output, writes.Load(), f.requests.Load(), err)
	}
}
