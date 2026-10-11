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

// Failure inventory: an unloaded call executes nothing; a miss grants nothing;
// failed persistence prevents continuation; revision drift blocks recovery;
// bounded search/activation cannot bypass policy, scope, or the Run Budget.
func TestLazySchemaWireActivationAndRecovery(t *testing.T) {
	var executed atomic.Int32
	f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 10, MaxToolCalls: 8}, func(round int, input wireRequest) any {
		has := func(name string) bool {
			for _, definition := range input.Tools {
				if definition["function"].(map[string]any)["name"] == name {
					return true
				}
			}
			return false
		}
		if !has("tool_search") {
			t.Error("large Catalog did not expose bounded discovery")
		}
		switch round {
		case 1:
			if has("calculator") || len(input.Tools) > 2 {
				t.Error("deferred Schema eagerly entered the request")
			}
			return call("calculator", `{"expression":"1 + 1"}`)
		case 2:
			if executed.Load() != 0 || !strings.Contains(input.Messages[len(input.Messages)-1].Content, "tool_not_loaded") {
				t.Error("unloaded call bypassed activation")
			}
			return call("tool_search", `{"query":"calculator expression"}`)
		case 3, 5:
			if !has("calculator") || len(input.Tools) > 3 {
				t.Error("next request did not load the selected full Schema")
			}
			return call("calculator", `{"expression":"1 + 1"}`)
		case 4:
			return modelResponse(provider.ChatChoice{Content: "partial"}, "length")
		default:
			return answer("2")
		}
	})
	f.request.Catalog = discoveryCatalog(t, &executed)
	f.request.AgentID = "agent_planner"
	f.request.SchemaConfig = domain.ToolSchemaConfig{Mode: "auto"}
	f.request.Sink = eventpkg.StoreSink{Store: f.store}
	if _, err := f.execute(); failure.Describe(err).Code != "incomplete_output" {
		t.Fatalf("expected interruption after activation: %v", err)
	}
	f.ctx = eventpkg.WithScope(f.ctx, eventpkg.Scope{RunID: f.run.ID, ConversationID: f.run.ConversationID, StageID: "stage-fixed", TurnID: "turn-resumed"})
	output, err := f.execute()
	if err != nil || output != "2" || executed.Load() != 2 {
		t.Fatalf("output=%q executions=%d err=%v", output, executed.Load(), err)
	}
	items, _ := f.store.ListRunEvents(f.run.ID)
	var activated bool
	for _, item := range items {
		if string(item.Type) == "tool.discovery.updated" {
			encoded, _ := json.Marshal(item.Payload)
			activated = activated || strings.Contains(string(encoded), "calculator")
		}
	}
	if !activated {
		t.Fatal("activation cannot be reconstructed from durable events")
	}
	records, _ := f.store.ListModelRequestRecords(f.run.ID)
	if len(records) != 6 || records[0].Envelope.ToolCount >= records[2].Envelope.ToolCount {
		t.Fatalf("capture lost per-request Schema visibility: records=%d", len(records))
	}
	t.Logf("evidence run=%s mode=lazy source=local-http model_calls=6 unloaded_executions=0 resume=restored limitations=deterministic-provider", f.run.ID)
}

func discoveryCatalog(t *testing.T, executed *atomic.Int32) *tool.Catalog {
	t.Helper()
	bindings := []tool.Binding{{Descriptor: tool.Descriptor{Name: "calculator", Description: "Calculate an arithmetic expression", Parameters: tool.ObjectSchema(map[string]any{"expression": map[string]any{"type": "string", "description": "arithmetic expression"}}, []string{"expression"})}, Handler: func(context.Context, json.RawMessage) (any, error) {
		executed.Add(1)
		return map[string]int{"value": 2}, nil
	}}}
	for index := range 20 {
		bindings = append(bindings, tool.Binding{Descriptor: tool.Descriptor{Name: fmt.Sprintf("reports_%02d", index), Description: strings.Repeat("Generate a quarterly financial report. ", 80), Parameters: tool.ObjectSchema(nil, nil)}, Handler: func(context.Context, json.RawMessage) (any, error) { return "report", nil }})
	}
	catalog, err := tool.NewCatalogWithPolicy(policy.Policy{Version: "fixture", DefaultAction: policy.ActionAllow}, bindings...)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}
