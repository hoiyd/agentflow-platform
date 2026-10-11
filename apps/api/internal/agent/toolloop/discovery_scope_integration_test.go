package toolloop_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
)

func TestDiscoveryInvalidConfigurationAndMissingIdentity(t *testing.T) {
	for _, missing := range []string{"sink", "events", "run", "agent", "reserved", "mode", "threshold"} {
		t.Run(missing, func(t *testing.T) {
			f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 3}, func(int, wireRequest) any { return answer("must not run") })
			enableLazy(f)
			switch missing {
			case "sink":
				f.request.Sink = nil
			case "events":
				f.request.RunEvents = nil
			case "run":
				f.request.Trace.RunID = ""
			case "agent":
				f.request.AgentID = ""
			case "mode":
				f.request.SchemaConfig.Mode = "typo"
			case "threshold":
				f.request.SchemaConfig.SchemaTokenThreshold = -1
			case "reserved":
				var err error
				f.request.Catalog, err = f.request.Catalog.CloneWith(tool.Binding{Descriptor: tool.Descriptor{Name: "tool_search", Parameters: tool.ObjectSchema(nil, nil)}, Handler: func(context.Context, json.RawMessage) (any, error) { return nil, nil }})
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.execute(); err == nil || f.requests.Load() != 0 {
				t.Fatalf("requests=%d err=%v", f.requests.Load(), err)
			}
		})
	}
}

func TestDiscoveryAutoKeepsEagerForSmallOrExpensiveIndex(t *testing.T) {
	for _, threshold := range []int{100000, 1} {
		f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 3}, func(_ int, input wireRequest) any {
			if hasWireTool(input, "tool_search") || !hasWireTool(input, "calculator") {
				t.Error("auto increased small-Catalog cost")
			}
			return answer("direct")
		})
		// One short Schema is cheaper than a discovery Schema, even above threshold.
		catalog, err := tool.NewCatalogWithPolicy(policy.Policy{DefaultAction: policy.ActionAllow}, tool.Binding{Descriptor: tool.Descriptor{Name: "calculator", Parameters: tool.ObjectSchema(nil, nil)}, Handler: func(context.Context, json.RawMessage) (any, error) { return nil, nil }})
		if err != nil {
			t.Fatal(err)
		}
		f.request.Catalog, f.request.SchemaConfig = catalog, domain.ToolSchemaConfig{Mode: "auto", SchemaTokenThreshold: threshold}
		if _, err := f.execute(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDiscoveryStateDoesNotCrossAgentOrStage(t *testing.T) {
	for _, boundary := range []string{"agent", "stage"} {
		t.Run(boundary, func(t *testing.T) {
			f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 5}, func(round int, input wireRequest) any {
				if round == 1 {
					return call("tool_search", `{"query":"calculator"}`)
				}
				if round == 2 {
					return modelResponse(provider.ChatChoice{Content: "partial"}, "length")
				}
				if hasWireTool(input, "calculator") {
					t.Error("foreign activation entered this scope")
				}
				return answer("fresh")
			})
			enableLazy(f)
			if _, err := f.execute(); failure.Describe(err).Code != "incomplete_output" {
				t.Fatal(err)
			}
			if boundary == "agent" {
				f.request.AgentID = "agent_other"
			} else {
				f.request.Trace.StepID = "stage-other"
			}
			f.ctx = eventpkg.WithScope(f.ctx, eventpkg.Scope{RunID: f.run.ID, StageID: f.request.Trace.StepID, TurnID: "different-scope"})
			if output, err := f.execute(); err != nil || output != "fresh" {
				t.Fatalf("output=%q err=%v", output, err)
			}
		})
	}
}

func TestDiscoveryCancellationTruncationAndRunBudget(t *testing.T) {
	for _, boundary := range []string{"cancel", "truncated", "tool_budget"} {
		t.Run(boundary, func(t *testing.T) {
			var cancel context.CancelFunc
			f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 5, MaxToolCalls: 1}, func(round int, _ wireRequest) any {
				if round == 1 {
					if boundary == "cancel" {
						cancel()
					}
					return call("tool_search", `{"query":"calculator"}`)
				}
				return call("calculator", `{"expression":"1 + 1"}`)
			})
			enableLazy(f)
			var executed atomic.Int32
			f.request.Catalog = discoveryCatalog(t, &executed)
			f.ctx, cancel = context.WithCancel(f.ctx)
			defer cancel()
			if boundary == "truncated" {
				f.request.ExecutorOptions.DefaultPolicy.MaxResultBytes = 10
			}
			output, err := f.execute()
			if err == nil || output != "" || executed.Load() != 0 {
				t.Fatalf("output=%q executions=%d err=%v", output, executed.Load(), err)
			}
			if boundary == "truncated" && !strings.Contains(err.Error(), "truncated") {
				t.Fatal(err)
			}
		})
	}
}

func TestDiscoveryAutoResumeCannotSwitchToEagerAfterCandidateDrift(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(fmt.Sprintf("empty=%t", empty), func(t *testing.T) {
			f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 5}, func(round int, _ wireRequest) any {
				if round == 1 {
					return call("tool_search", `{"query":"calculator"}`)
				}
				return modelResponse(provider.ChatChoice{Content: "partial"}, "length")
			})
			enableLazy(f)
			f.request.SchemaConfig.Mode = "auto"
			var executed atomic.Int32
			f.request.Catalog = discoveryCatalog(t, &executed)
			if _, err := f.execute(); failure.Describe(err).Code != "incomplete_output" {
				t.Fatal(err)
			}
			// The current ready set shrank below auto's threshold. Existing visibility
			// still belongs to the old frozen candidate set and must fail closed.
			f.request.Catalog = tool.DefaultCatalog()
			if empty {
				var err error
				f.request.Catalog, err = tool.NewCatalog()
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.execute(); err == nil || f.requests.Load() != 2 {
				t.Fatalf("mode drift sent another request: requests=%d err=%v", f.requests.Load(), err)
			}
		})
	}
}
