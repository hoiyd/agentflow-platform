package toolloop_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/agent/toolloop"
	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/inference/capture"
	"agentflow-platform/apps/api/internal/inference/openai"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/testsupport/modelstream"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/artifact"
	"agentflow-platform/apps/api/internal/tool/policy"
	"agentflow-platform/apps/api/internal/tool/progress"
)

type loopFixture struct {
	store    *fixturestore.Store
	run      domain.Run
	ctx      context.Context
	client   *openai.Client
	request  toolloop.Request
	requests atomic.Int32
}

type wireRequest struct {
	Messages []provider.Message `json:"messages"`
	Tools    []map[string]any   `json:"tools"`
	Stream   bool               `json:"stream"`
}

func newLoopFixture(t *testing.T, limits domain.RuntimeRunBudget, respond func(int, wireRequest) any) *loopFixture {
	t.Helper()
	f := &loopFixture{store: fixturestore.New()}
	conversation, err := f.store.CreateConversation("bounded Tool loop fixture")
	if err != nil {
		t.Fatal(err)
	}
	f.run, err = f.store.CreateRunWithContract("agent_planner", conversation.ID, domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &limits,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input wireRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		output := respond(int(f.requests.Add(1)), input)
		if !input.Stream {
			t.Error("Tool fixture received a non-streaming request")
		}
		stream, err := modelstream.Completion(output)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, stream)
	}))
	t.Cleanup(server.Close)
	f.client = openai.NewClientWithTimeout("fixture-not-a-secret", server.URL, "fixture-model", time.Second)
	f.client.SetRetryPolicy(openai.RetryPolicy{MaxAttempts: 1})
	f.client.SetRequestRecorder(capture.NewRecorder(f.store, capture.Options{Mode: domain.ModelRequestCaptureFull}))
	f.ctx = eventpkg.WithScope(context.Background(), eventpkg.Scope{
		RunID: f.run.ID, ConversationID: conversation.ID, StageID: "stage-fixed", TurnID: "turn-first",
	})
	f.ctx = budget.WithController(f.ctx, budget.NewTracker(f.store, eventpkg.StoreSink{Store: f.store}, f.run))
	f.ctx = contextassembly.WithSession(f.ctx, contextassembly.Session{
		Config: contextassembly.DefaultConfig(), Sink: eventpkg.StoreSink{Store: f.store}, CurrentInput: "solve using tools",
	})
	f.ctx = progress.WithGuard(f.ctx, progress.New(progress.DefaultConfig()))
	f.request = toolloop.Request{Latest: "solve using tools", Catalog: tool.DefaultCatalog(),
		Trace:           provider.ChatTrace{Recorder: eventpkg.NewRecorder(f.store), RunID: f.run.ID, StepID: "stage-fixed"},
		ExecutorOptions: tool.ExecutorOptions{ArtifactStore: f.store, EffectJournal: f.store},
		RunEvents:       func() ([]domain.RunEvent, error) { return f.store.ListRunEvents(f.run.ID) },
	}
	encoded, _ := json.Marshal(limits)
	t.Logf("protocol fixture run=%s stage=stage-fixed model=fixture-model budget=%s capture=full provider=local-http", f.run.ID, encoded)
	return f
}

func answer(content string) any { return modelResponse(provider.ChatChoice{Content: content}, "stop") }

func call(name, arguments string) any {
	return modelResponse(provider.ChatChoice{ToolCalls: []provider.ToolCall{{
		ID: "provider-reused-id", Type: "function", Function: provider.FunctionCall{Name: name, Arguments: arguments},
	}}}, "tool_calls")
}

func modelResponse(choice provider.ChatChoice, reason string) any {
	return map[string]any{"choices": []any{map[string]any{"message": map[string]any{
		"role": "assistant", "content": choice.Content, "tool_calls": choice.ToolCalls,
	}, "finish_reason": reason}}, "usage": map[string]int{"prompt_tokens": 10, "completion_tokens": 2, "total_tokens": 12}}
}

func (f *loopFixture) execute() (string, error) {
	events, errs := toolloop.Stream(f.ctx, f.client, f.request)
	var output strings.Builder
	for item := range events {
		if item.Type == "delta" {
			if item.Reset {
				output.Reset()
			}
			output.WriteString(item.Delta)
		}
	}
	return output.String(), <-errs
}

func TestMultiRoundDependentCallsAndSchemaCorrection(t *testing.T) {
	for _, first := range []string{`{"expression":"1 + 1"}`, `{}`} {
		t.Run(first, func(t *testing.T) {
			f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 4, MaxToolCalls: 4}, func(round int, input wireRequest) any {
				if len(input.Tools) == 0 {
					t.Error("follow-up dropped frozen Tool definitions")
				}
				switch round {
				case 1:
					return call("calculator", first)
				case 2:
					last := input.Messages[len(input.Messages)-1]
					if last.Role != "tool" || last.ToolCallID != input.Messages[len(input.Messages)-2].ToolCalls[0].ID {
						t.Error("unpaired Tool observation")
					}
					if first == "{}" && !strings.Contains(last.Content, "invalid_arguments") {
						t.Error("schema failure missing from observation")
					}
					if first != "{}" && !strings.Contains(last.Content, `"value":2`) {
						t.Error("first result unavailable to dependent call")
					}
					return call("calculator", `{"expression":"2 + 3"}`)
				default:
					if len(input.Messages) < 6 || !strings.Contains(input.Messages[len(input.Messages)-1].Content, `"value":5`) {
						t.Error("second result missing")
					}
					return answer("5")
				}
			})
			output, err := f.execute()
			if err != nil || output != "5" || f.requests.Load() != 3 {
				t.Fatalf("output=%q requests=%d err=%v", output, f.requests.Load(), err)
			}
			ledger, _, _ := f.store.GetRunUsageLedger(f.run.ID)
			wantTools := 2
			if first == "{}" {
				wantTools = 1
			}
			if ledger.Totals.ModelCalls != 3 || ledger.Totals.ToolCalls != wantTools || ledger.Totals.OpenReservations != 0 {
				t.Fatalf("ledger=%#v", ledger.Totals)
			}
			items, _ := f.store.ListRunEvents(f.run.ID)
			manifests, calls := map[string]bool{}, map[string]bool{}
			for _, item := range items {
				if item.Type == domain.EventContextAssembled {
					encoded, _ := json.Marshal(item.Payload["manifest"])
					var manifest domain.ContextManifest
					_ = json.Unmarshal(encoded, &manifest)
					manifests[manifest.ID] = true
				}
				if item.Type == domain.EventToolStarted {
					calls[item.Payload["tool_call_id"].(string)] = true
				}
			}
			if len(manifests) != 3 || len(calls) != 2 {
				t.Fatalf("manifests=%d distinct calls=%d", len(manifests), len(calls))
			}
			records, _ := f.store.ListModelRequestRecords(f.run.ID)
			if len(records) != 3 {
				t.Fatalf("physical attempts=%d", len(records))
			}
			for _, record := range records {
				if record.Envelope.ToolCount == 0 || record.Envelope.ContextManifestID == "" {
					t.Fatalf("capture lost contract: %#v", record.Envelope)
				}
			}
		})
	}
}

func TestMultiRoundReadsArtifactProducedInPreviousRound(t *testing.T) {
	f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 4, MaxToolCalls: 4}, func(round int, input wireRequest) any {
		switch round {
		case 1:
			return call("large_result", `{}`)
		case 2:
			var result tool.ExecutionResult
			if err := json.Unmarshal([]byte(input.Messages[len(input.Messages)-1].Content), &result); err != nil || result.Artifact == nil {
				t.Errorf("missing artifact: %#v err=%v", result, err)
				return answer("missing")
			}
			return call("artifact_read", fmt.Sprintf(`{"artifact_id":%q,"limit":128}`, result.Artifact.ID))
		default:
			if !strings.Contains(input.Messages[len(input.Messages)-1].Content, "evidence") {
				t.Error("artifact was not read")
			}
			return answer("evidence read")
		}
	})
	bindings := artifact.NewService(f.store, f.request.Trace.Recorder).ToolBindings()
	bindings = append(bindings, tool.Binding{Descriptor: tool.Descriptor{Name: "large_result", Parameters: tool.ObjectSchema(nil, nil)},
		Policy: tool.ExecutionPolicy{MaxResultBytes: 128}, Handler: func(context.Context, json.RawMessage) (any, error) { return strings.Repeat("evidence ", 500), nil }})
	catalog, err := tool.NewCatalogWithPolicy(policy.Policy{Version: "fixture", DefaultAction: policy.ActionAllow}, bindings...)
	if err != nil {
		t.Fatal(err)
	}
	f.request.Catalog = catalog
	output, err := f.execute()
	if err != nil || output != "evidence read" || f.requests.Load() != 3 {
		t.Fatalf("output=%q requests=%d err=%v", output, f.requests.Load(), err)
	}
}

func TestMultiRoundStopsAtExistingBudgets(t *testing.T) {
	for _, limits := range []domain.RuntimeRunBudget{{MaxModelCalls: 2}, {MaxModelCalls: 8, MaxToolCalls: 1}} {
		t.Run(fmt.Sprint(limits), func(t *testing.T) {
			f := newLoopFixture(t, limits, func(round int, _ wireRequest) any {
				return call("calculator", fmt.Sprintf(`{"expression":"%d + 1"}`, round))
			})
			output, err := f.execute()
			if _, ok := budget.AsExceeded(err); !ok || output != "" || f.requests.Load() != 2 {
				t.Fatalf("output=%q requests=%d err=%v", output, f.requests.Load(), err)
			}
		})
	}
}

func TestMultiRoundProtocolFailuresExecuteNothing(t *testing.T) {
	for _, test := range []struct {
		name     string
		response any
		code     string
	}{
		{"unfinished generation", modelResponse(provider.ChatChoice{ToolCalls: []provider.ToolCall{{ID: "x", Type: "function", Function: provider.FunctionCall{Name: "calculator", Arguments: `{"expression":`}}}}, "length"), "incomplete_output"},
		{"malformed arguments", call("calculator", `{"expression":`), "tool_call_protocol_invalid"},
		{"duplicate IDs", modelResponse(provider.ChatChoice{ToolCalls: []provider.ToolCall{
			{ID: "duplicate", Type: "function", Function: provider.FunctionCall{Name: "calculator", Arguments: `{"expression":"1 + 1"}`}},
			{ID: "duplicate", Type: "function", Function: provider.FunctionCall{Name: "calculator", Arguments: `{"expression":"2 + 2"}`}},
		}}, "tool_calls"), "tool_call_protocol_invalid"},
		{"empty final answer", answer(""), "invalid_response"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 2}, func(int, wireRequest) any { return test.response })
			output, err := f.execute()
			ledger, _, _ := f.store.GetRunUsageLedger(f.run.ID)
			if err == nil || failure.Describe(err).Code != test.code || output != "" || ledger.Totals.ToolCalls != 0 || f.requests.Load() != 1 {
				t.Fatalf("output=%q ledger=%#v requests=%d err=%v", output, ledger.Totals, f.requests.Load(), err)
			}
		})
	}
}

func TestMultiRoundRejectsUnboundedToolExecution(t *testing.T) {
	f := newLoopFixture(t, domain.RuntimeRunBudget{}, func(int, wireRequest) any { return call("calculator", `{"expression":"1 + 1"}`) })
	_, err := f.execute()
	ledger, _, _ := f.store.GetRunUsageLedger(f.run.ID)
	if failure.Describe(err).Code != "tool_loop_unbounded" || ledger.Totals.ToolCalls != 0 {
		t.Fatalf("ledger=%#v err=%v", ledger.Totals, err)
	}
}

func TestMultiRoundNoProgressHaltsWithoutSuccessfulAnswer(t *testing.T) {
	f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 10}, func(int, wireRequest) any { return call("calculator", `{"expression":"1 + 1"}`) })
	f.ctx = progress.WithGuard(f.ctx, progress.New(progress.Config{Version: progress.CurrentVersion, Enabled: true, WarnAfter: 1, BlockAfter: 2, HaltAfter: 3, HistoryMax: 8}))
	output, err := f.execute()
	if failure.Describe(err).Code != string(tool.ErrorNoProgress) || output != "" || f.requests.Load() > 5 {
		t.Fatalf("output=%q requests=%d err=%v", output, f.requests.Load(), err)
	}
}

func TestMultiRoundCancellationDuringToolStopsContinuation(t *testing.T) {
	f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 4}, func(int, wireRequest) any { return call("cancel_tool", `{}`) })
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	f.ctx = ctx
	catalog, err := tool.NewCatalog(tool.Binding{Descriptor: tool.Descriptor{Name: "cancel_tool", Parameters: tool.ObjectSchema(nil, nil)}, Handler: func(ctx context.Context, _ json.RawMessage) (any, error) { cancel(); return nil, ctx.Err() }})
	if err != nil {
		t.Fatal(err)
	}
	f.request.Catalog = catalog
	output, err := f.execute()
	if !errors.Is(err, context.Canceled) || output != "" || f.requests.Load() != 1 {
		t.Fatalf("output=%q requests=%d err=%v", output, f.requests.Load(), err)
	}
}

func TestMultiRoundStageRetryDoesNotRepeatCommittedWrite(t *testing.T) {
	var writes atomic.Int32
	f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 8, MaxToolCalls: 4}, func(round int, _ wireRequest) any {
		if round == 1 || round == 3 {
			return call("external_write", `{}`)
		}
		if round == 2 {
			return modelResponse(provider.ChatChoice{Content: "partial"}, "length")
		}
		if round == 5 {
			return call("external_write", `{"value":2}`)
		}
		return answer("committed")
	})
	capability := policy.Capability{SideEffect: policy.SideEffectExternalWrite}
	catalog, err := tool.NewCatalogWithPolicy(policy.Policy{Version: "fixture", DefaultAction: policy.ActionAllow, Rules: []policy.Rule{{ID: "fixture-write", Tool: "external_write", Action: policy.ActionAllow, Capability: capability}}}, tool.Binding{
		Descriptor: tool.Descriptor{Name: "external_write", Parameters: tool.ObjectSchema(map[string]any{"value": map[string]any{"type": "integer"}}, nil),
			Security: capability},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			writes.Add(1)
			return map[string]string{"receipt": "committed"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.request.Catalog = catalog
	if _, err := f.execute(); failure.Describe(err).Code != "incomplete_output" {
		t.Fatalf("first Stage should fail after write: %v", err)
	}
	f.ctx = eventpkg.WithScope(f.ctx, eventpkg.Scope{RunID: f.run.ID, ConversationID: f.run.ConversationID, StageID: "stage-fixed", TurnID: "turn-resumed"})
	output, err := f.execute()
	if err != nil || output != "committed" || writes.Load() != 1 {
		t.Fatalf("output=%q writes=%d err=%v", output, writes.Load(), err)
	}
	// A different argument in the same logical slot must not reuse the receipt
	// or perform another write, even when a retry model changes its plan.
	output, err = f.execute()
	if failure.Describe(err).Code != string(tool.ErrorEffectJournal) || output != "" || writes.Load() != 1 || f.requests.Load() != 5 {
		t.Fatalf("argument drift accepted: output=%q writes=%d requests=%d err=%v", output, writes.Load(), f.requests.Load(), err)
	}
}

func TestMultiRoundReadFailureCanBeCorrectedButUncertainWriteCannot(t *testing.T) {
	for _, test := range []struct {
		external bool
		code     tool.ErrorCode
	}{{false, tool.ErrorNoResults}, {true, tool.ErrorNoResults}, {true, tool.ErrorInvalidArgs}} {
		t.Run(fmt.Sprint(test), func(t *testing.T) {
			f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 4}, func(round int, input wireRequest) any {
				if round == 1 {
					return call("failing_tool", `{}`)
				}
				if !strings.Contains(input.Messages[len(input.Messages)-1].Content, "no_results") {
					t.Error("missing typed failure observation")
				}
				return answer("no evidence available")
			})
			descriptor := tool.Descriptor{Name: "failing_tool", Parameters: tool.ObjectSchema(nil, nil)}
			if test.external {
				descriptor.Security.SideEffect = policy.SideEffectExternalWrite
			}
			security := policy.Policy{Version: "fixture", DefaultAction: policy.ActionAllow, Rules: []policy.Rule{{ID: "fixture-tool", Tool: descriptor.Name, Action: policy.ActionAllow, Capability: descriptor.Security}}}
			catalog, err := tool.NewCatalogWithPolicy(security, tool.Binding{Descriptor: descriptor, Handler: func(context.Context, json.RawMessage) (any, error) {
				return nil, &tool.ExecutionError{Code: test.code, Message: "no evidence"}
			}})
			if err != nil {
				t.Fatal(err)
			}
			f.request.Catalog = catalog
			output, err := f.execute()
			if test.external {
				if err == nil || output != "" || f.requests.Load() != 1 {
					t.Fatalf("uncertain write continued: output=%q requests=%d err=%v", output, f.requests.Load(), err)
				}
				effects, _ := f.store.ListToolEffects(f.run.ID)
				if len(effects) != 1 || effects[0].Status == domain.ToolEffectCommitted {
					t.Fatalf("uncertainty lost: %#v", effects)
				}
			} else if err != nil || output != "no evidence available" || f.requests.Load() != 2 {
				t.Fatalf("read error not correctable: output=%q err=%v", output, err)
			}
		})
	}
}

func TestMultiRoundContextExhaustionStopsBeforeNextModelCall(t *testing.T) {
	f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 4}, func(int, wireRequest) any { return call("large_result", `{}`) })
	config := contextassembly.DefaultConfig()
	config.ContextWindowTokens = 700
	config.OutputReserveTokens = 16
	config.SafetyMarginTokens = 8
	f.ctx = contextassembly.WithSession(f.ctx, contextassembly.Session{Config: config, Sink: eventpkg.StoreSink{Store: f.store}, CurrentInput: "solve using tools"})
	catalog, err := tool.NewCatalog(tool.Binding{Descriptor: tool.Descriptor{Name: "large_result", Parameters: tool.ObjectSchema(nil, nil)}, Policy: tool.ExecutionPolicy{MaxResultBytes: 16384}, Handler: func(context.Context, json.RawMessage) (any, error) { return strings.Repeat("x", 10000), nil }})
	if err != nil {
		t.Fatal(err)
	}
	f.request.Catalog = catalog
	output, err := f.execute()
	if !errors.Is(err, contextassembly.ErrInputBudgetExceeded) || output != "" || f.requests.Load() != 1 {
		t.Fatalf("output=%q requests=%d err=%v", output, f.requests.Load(), err)
	}
	items, _ := f.store.ListRunEvents(f.run.ID)
	assemblies := 0
	for _, item := range items {
		if item.Type == domain.EventContextAssembled {
			assemblies++
		}
	}
	if assemblies != 2 {
		t.Fatalf("missing failed assembly diagnostic: %d", assemblies)
	}
}
