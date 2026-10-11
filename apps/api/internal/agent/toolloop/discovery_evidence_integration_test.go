package toolloop_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
)

// This ablation measures real serialized requests, not live selection quality.
func TestDiscoveryControlledSchemaCostEvidence(t *testing.T) {
	reports := []map[string]any{}
	costs := map[string]int{}
	for _, mode := range []string{"eager", "lazy"} {
		var executed atomic.Int32
		f := newLoopFixture(t, domain.RuntimeRunBudget{MaxModelCalls: 5, MaxToolCalls: 4}, func(_ int, input wireRequest) any {
			if !hasWireTool(input, "calculator") {
				return call("tool_search", `{"query":"calculator expression"}`)
			}
			if executed.Load() == 0 {
				return call("calculator", `{"expression":"1 + 1"}`)
			}
			return answer("2")
		})
		enableLazy(f)
		f.request.SchemaConfig.Mode = mode
		f.request.Catalog = discoveryCatalog(t, &executed)
		started := time.Now()
		output, err := f.execute()
		if err != nil || output != "2" || executed.Load() != 1 {
			t.Fatalf("mode=%s output=%q executions=%d err=%v", mode, output, executed.Load(), err)
		}
		records, err := f.store.ListModelRequestRecords(f.run.ID)
		if err != nil {
			t.Fatal(err)
		}
		requests := []map[string]any{}
		items, err := f.store.ListRunEvents(f.run.ID)
		if err != nil {
			t.Fatal(err)
		}
		manifests := map[string]domain.ContextManifest{}
		for _, item := range items {
			if item.Type != domain.EventContextAssembled {
				continue
			}
			data, err := json.Marshal(item.Payload)
			if err != nil {
				t.Fatal(err)
			}
			var payload eventpkg.ContextAssembledPayload
			if err := json.Unmarshal(data, &payload); err != nil {
				t.Fatal(err)
			}
			manifests[payload.Manifest.ID] = payload.Manifest
		}
		for _, record := range records {
			schema := record.Envelope.SourceTokenBreakdown["tool_definition"]
			costs[mode] += schema
			manifest := manifests[record.Envelope.ContextManifestID]
			if manifest.InputBudgetTokens == 0 {
				t.Fatal("request missing actual Manifest budget")
			}
			requests = append(requests, map[string]any{"payload_hash": record.Envelope.PayloadHash, "model_call_id": record.Envelope.ModelCallID, "visible_tools": record.Envelope.ToolCount, "schema_estimated_tokens": schema, "input_budget_tokens": manifest.InputBudgetTokens, "schema_fraction_of_input_budget": float64(schema) / float64(manifest.InputBudgetTokens)})
		}
		reports = append(reports, map[string]any{"mode": mode, "run_id": f.run.ID, "input": f.request.Latest, "config": f.request.SchemaConfig.Normalize(), "candidates": f.request.Catalog.Definitions(), "budget": domain.RuntimeRunBudget{MaxModelCalls: 5, MaxToolCalls: 4}, "output": output, "success": true, "search_calls": f.requests.Load() - 2, "model_calls": f.requests.Load(), "elapsed_ms": time.Since(started).Milliseconds(), "requests": requests, "schema_estimated_tokens_total": costs[mode], "cached_usage": "unknown", "limitations": []string{"deterministic local HTTP provider; no real selection quality or cost claim", "latency is fixture wall time; not a provider benchmark", "per-call fixture usage is fixed, not tokenizer evidence"}})
	}
	if costs["lazy"] == 0 || costs["lazy"] >= costs["eager"] {
		t.Fatalf("Schema cost not reduced: %#v", costs)
	}
	data, err := json.MarshalIndent(reports, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(t.TempDir(), "schema-cost-evidence.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("controlled_evidence=%s", data)
}
