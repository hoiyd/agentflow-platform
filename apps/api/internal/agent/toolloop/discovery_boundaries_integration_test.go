package toolloop_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/availability"
	"agentflow-platform/apps/api/internal/tool/policy"
	"agentflow-platform/apps/api/internal/tool/progress"
)

func enableLazy(f *loopFixture) {
	f.request.SchemaConfig = domain.ToolSchemaConfig{Mode: "lazy"}
	f.request.AgentID = "agent_planner"
	f.request.Sink = eventpkg.StoreSink{Store: f.store}
}

func hasWireTool(input wireRequest, name string) bool {
	for _, definition := range input.Tools {
		if definition["function"].(map[string]any)["name"] == name {
			return true
		}
	}
	return false
}

func providerBatch(first, firstArgs, second, secondArgs string) provider.ChatChoice {
	return provider.ChatChoice{ToolCalls: []provider.ToolCall{
		{ID: "first", Type: "function", Function: provider.FunctionCall{Name: first, Arguments: firstArgs}},
		{ID: "second", Type: "function", Function: provider.FunctionCall{Name: second, Arguments: secondArgs}},
	}}
}

func TestDiscoveryMissAndArgumentCorrection(t *testing.T) {
	for _, args := range []string{`{"query":".*"}`, `{"query":"unregistered_secret_tool"}`, `{"query":""}`, `{"query":42}`, `{"query":"calculator","extra":true}`, fmt.Sprintf(`{"query":%q}`, strings.Repeat("x", 257))} {
		t.Run(args[:min(len(args), 60)], func(t *testing.T) {
			f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 4, MaxToolCalls: 3}, func(round int, input wireRequest) any {
				if round == 1 {
					return call("tool_search", args)
				}
				if round == 2 {
					if hasWireTool(input, "calculator") {
						t.Error("miss/invalid query expanded visible definitions")
					}
					return call("tool_search", `{"query":"calculator expression"}`)
				}
				if !hasWireTool(input, "calculator") {
					t.Error("corrected search did not activate Schema")
				}
				return answer("corrected")
			})
			enableLazy(f)
			if output, err := f.execute(); err != nil || output != "corrected" {
				t.Fatalf("output=%q err=%v", output, err)
			}
		})
	}
}

func TestDiscoveryPersistenceAndRestoreFailClosed(t *testing.T) {
	for _, reason := range []string{"initial_visibility", "search_admitted", "search_match", "restore_read"} {
		t.Run(reason, func(t *testing.T) {
			f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 4}, func(round int, _ wireRequest) any {
				if round == 1 {
					return call("tool_search", `{"query":"calculator expression"}`)
				}
				return answer("must not continue")
			})
			enableLazy(f)
			f.request.Sink = eventpkg.SinkFunc(func(ctx context.Context, item domain.RunEvent) error {
				if item.Payload["reason"] == reason {
					return errors.New("fixture state write failed")
				}
				return (eventpkg.StoreSink{Store: f.store}).Publish(ctx, item)
			})
			if reason == "restore_read" {
				f.request.RunEvents = func() ([]domain.RunEvent, error) { return nil, errors.New("fixture state read failed") }
			}
			if output, err := f.execute(); err == nil || output != "" || f.requests.Load() > 1 {
				t.Fatalf("output=%q requests=%d err=%v", output, f.requests.Load(), err)
			}
		})
	}
}

func TestDiscoveryAuthorizationAndDefinitionRevocation(t *testing.T) {
	for _, revoke := range []string{"discovery", "next_request"} {
		t.Run(revoke, func(t *testing.T) {
			var checks atomic.Int32
			f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 4}, func(round int, input wireRequest) any {
				if round == 1 {
					return call("tool_search", `{"query":"calculator expression"}`)
				}
				if hasWireTool(input, "calculator") {
					t.Error("revoked Schema was sent to the provider")
				}
				return answer("unavailable")
			})
			enableLazy(f)
			f.request.ExecutorOptions.Authorize = func(_ context.Context, call tool.ExecutionRequest, _ policy.Scope) error {
				if call.Tool == "calculator" && (revoke == "discovery" || checks.Add(1) >= 3) {
					return &availability.DeniedError{Reason: "workspace_disabled"}
				}
				return nil
			}
			output, err := f.execute()
			if revoke == "discovery" {
				if err != nil || output != "unavailable" {
					t.Fatalf("output=%q err=%v", output, err)
				}
			} else if err == nil || output != "" || f.requests.Load() != 1 {
				t.Fatalf("revocation did not block next request: output=%q requests=%d err=%v", output, f.requests.Load(), err)
			}
		})
	}
}

func TestDiscoverySameBatchDoesNotGrantExecution(t *testing.T) {
	var executed atomic.Int32
	f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 4}, func(round int, input wireRequest) any {
		if round == 1 {
			return modelResponse(providerBatch("tool_search", `{"query":"calculator"}`, "calculator", `{"expression":"1 + 1"}`), "tool_calls")
		}
		if round == 2 {
			if executed.Load() != 0 || !strings.Contains(input.Messages[len(input.Messages)-1].Content, "tool_not_loaded") {
				t.Error("same-batch discovery granted premature execution")
			}
			return call("calculator", `{"expression":"1 + 1"}`)
		}
		return answer("2")
	})
	enableLazy(f)
	f.request.Catalog = discoveryCatalog(t, &executed)
	if output, err := f.execute(); err != nil || output != "2" || executed.Load() != 1 {
		t.Fatalf("output=%q executions=%d err=%v", output, executed.Load(), err)
	}
}

func TestDiscoverySearchBudgetSurvivesRestart(t *testing.T) {
	f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 20, MaxToolCalls: 20}, func(round int, _ wireRequest) any {
		if round == 5 {
			return modelResponse(provider.ChatChoice{Content: "partial"}, "length")
		}
		return call("tool_search", `{"query":"no_match"}`)
	})
	enableLazy(f)
	f.ctx = progress.WithGuard(f.ctx, progress.New(progress.Config{Version: progress.CurrentVersion, Enabled: false}))
	if _, err := f.execute(); failure.Describe(err).Code != "incomplete_output" {
		t.Fatal(err)
	}
	f.ctx = eventpkg.WithScope(f.ctx, eventpkg.Scope{RunID: f.run.ID, ConversationID: f.run.ConversationID, StageID: "stage-fixed", TurnID: "turn-resumed"})
	if output, err := f.execute(); failure.Describe(err).Code != string(tool.ErrorBudgetExceeded) || output != "" || f.requests.Load() != 10 {
		t.Fatalf("output=%q requests=%d err=%v", output, f.requests.Load(), err)
	}
}

func TestDiscoveryRestoreRejectsDriftAndMalformedState(t *testing.T) {
	for _, mutation := range []string{"candidate", "mode", "count", "duplicate", "unknown", "revision"} {
		t.Run(mutation, func(t *testing.T) {
			f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 4}, func(round int, _ wireRequest) any {
				if round == 1 {
					return call("tool_search", `{"query":"calculator"}`)
				}
				return modelResponse(provider.ChatChoice{Content: "partial"}, "length")
			})
			enableLazy(f)
			if _, err := f.execute(); failure.Describe(err).Code != "incomplete_output" {
				t.Fatal(err)
			}
			items, _ := f.store.ListRunEvents(f.run.ID)
			var latest domain.RunEvent
			for _, item := range items {
				if item.Type == domain.EventToolDiscoveryUpdated {
					latest = item
				}
			}
			data, _ := json.Marshal(latest.Payload)
			var state domain.ToolDiscoveryState
			if err := json.Unmarshal(data, &state); err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "candidate":
				state.CandidateDigest = "changed"
			case "mode":
				state.Mode = "eager"
			case "count":
				state.SearchCalls = -1
			case "duplicate":
				state.Active = append(state.Active, state.Active[0])
			case "unknown":
				state.Active[0].Name = "foreign_workspace_tool"
			case "revision":
				state.Active[0].Revision = "changed"
			}
			item, err := eventpkg.NewRunEvent(domain.EventToolDiscoveryUpdated, eventpkg.EventMetadata{RunID: f.run.ID, StageID: "stage-fixed", TurnID: "turn-first"}, eventpkg.ToolDiscoveryPayload{ToolDiscoveryState: state})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.CreateRunEvent(item); err != nil {
				t.Fatal(err)
			}
			if _, err := f.execute(); err == nil || f.requests.Load() != 2 {
				t.Fatalf("invalid state reached model: requests=%d err=%v", f.requests.Load(), err)
			}
		})
	}
}

func TestDiscoveryResumePreservesCommittedEffectIdentity(t *testing.T) {
	for _, drift := range []bool{false, true} {
		t.Run(fmt.Sprintf("argument_drift=%t", drift), func(t *testing.T) {
			var writes atomic.Int32
			f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 8, MaxToolCalls: 4}, func(round int, input wireRequest) any {
				if !hasWireTool(input, "external_write") {
					return call("tool_search", `{"query":"external_write"}`)
				}
				if round == 3 {
					return modelResponse(provider.ChatChoice{Content: "partial"}, "length")
				}
				if round == 5 {
					return answer("committed")
				}
				args := `{"value":1}`
				if round == 4 && drift {
					args = `{"value":2}`
				}
				return call("external_write", args)
			})
			enableLazy(f)
			capability := policy.Capability{SideEffect: policy.SideEffectExternalWrite}
			catalog, err := tool.NewCatalogWithPolicy(policy.Policy{DefaultAction: policy.ActionAllow, Rules: []policy.Rule{{ID: "fixture-write", Tool: "external_write", Action: policy.ActionAllow, Capability: capability}}}, tool.Binding{
				Descriptor: tool.Descriptor{Name: "external_write", Parameters: tool.ObjectSchema(map[string]any{"value": map[string]any{"type": "integer"}}, []string{"value"}), Security: capability},
				Handler:    func(context.Context, json.RawMessage) (any, error) { writes.Add(1); return "committed", nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			f.request.Catalog = catalog
			if _, err := f.execute(); failure.Describe(err).Code != "incomplete_output" {
				t.Fatal(err)
			}
			f.ctx = eventpkg.WithScope(f.ctx, eventpkg.Scope{RunID: f.run.ID, StageID: "stage-fixed", TurnID: "turn-recovered"})
			output, err := f.execute()
			if writes.Load() != 1 {
				t.Fatalf("committed write repeated: %d", writes.Load())
			}
			if drift {
				if failure.Describe(err).Code != string(tool.ErrorEffectJournal) {
					t.Fatalf("argument drift accepted: %v", err)
				}
			} else if err != nil || output != "committed" {
				t.Fatalf("output=%q err=%v", output, err)
			}
		})
	}
}
