package toolloop_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
)

func TestDiscoveryBoundedIndexStillSearchesUnlistedCandidates(t *testing.T) {
	f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 5, MaxToolCalls: 4}, func(round int, input wireRequest) any {
		switch round {
		case 1:
			description := input.Tools[0]["function"].(map[string]any)["description"].(string)
			if !strings.Contains(description, "Index abbreviated") || strings.Contains(description, "zz_unlisted") {
				t.Error("index was not bounded")
			}
			return call("tool_search", `{"query":"special_parameter special_parameter"}`)
		case 2:
			if !hasWireTool(input, "zz_unlisted") {
				t.Error("abbreviated metadata restricted search")
			}
			return call("tool_search", `{"query":"reports"}`)
		case 3:
			if !hasWireTool(input, "reports_000") || !hasWireTool(input, "reports_002") || hasWireTool(input, "reports_003") {
				t.Error("results not bounded/deterministic")
			}
			return call("tool_search", `{"query":"reports"}`)
		default:
			if len(input.Tools) != 5 {
				t.Error("repeated discovery grew active Schema set")
			}
			return answer("bounded")
		}
	})
	enableLazy(f)
	bindings := []tool.Binding{}
	for i := range 250 {
		bindings = append(bindings, tool.Binding{Descriptor: tool.Descriptor{Name: fmt.Sprintf("reports_%03d", i), Parameters: tool.ObjectSchema(nil, nil)}, Handler: func(context.Context, json.RawMessage) (any, error) { return "report", nil }})
	}
	bindings = append(bindings, tool.Binding{Descriptor: tool.Descriptor{Name: "zz_unlisted", Parameters: tool.ObjectSchema(map[string]any{"special_parameter": map[string]any{"type": "string"}}, nil)}, Handler: func(context.Context, json.RawMessage) (any, error) { return "found", nil }})
	catalog, err := tool.NewCatalogWithPolicy(policy.Policy{DefaultAction: policy.ActionAllow}, bindings...)
	if err != nil {
		t.Fatal(err)
	}
	f.request.Catalog = catalog
	if output, err := f.execute(); err != nil || output != "bounded" {
		t.Fatalf("output=%q err=%v", output, err)
	}
	if summary := catalog.DiscoveryIndex().Summary(4096); len(summary) > 4096 {
		t.Fatalf("summary bytes=%d", len(summary))
	}
	if summary := catalog.DiscoveryIndex().Summary(1); summary != "" {
		t.Fatalf("tiny summary overflow: %q", summary)
	}
}
