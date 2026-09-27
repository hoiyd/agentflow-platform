package tool_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/testsupport/tooltest"
	"agentflow-platform/apps/api/internal/tool"
)

func TestDefaultBindingsSatisfyContractHarness(t *testing.T) {
	catalog, err := tool.BuildCatalog(tool.DefaultConfig(), tool.WebSearchTool(nil))
	if err != nil {
		t.Fatal(err)
	}
	specs := map[string]tooltest.BindingContract{
		"calculator": {
			ValidArguments: json.RawMessage(`{"expression":"2 + 2"}`),
			GoodResult:     map[string]any{"expression": "2 + 2", "value": float64(4)},
			InvalidCalls: []tooltest.InvalidCall{
				{Name: "missing expression", Arguments: json.RawMessage(`{}`), WantArgumentCode: "required"},
				{Name: "unknown field", Arguments: json.RawMessage(`{"expression":"2 + 2","precision":2}`), WantArgumentCode: "additional_properties"},
			},
			BadResults: []tooltest.BadResult{
				{Name: "missing value", Value: map[string]any{"expression": "2 + 2"}},
				{Name: "non numeric value", Value: map[string]any{"expression": "2 + 2", "value": "four"}},
			},
			ValidateResult: validateCalculatorResult,
		},
		"get_current_time": {
			ValidArguments: json.RawMessage(`{"timezone":"UTC"}`),
			GoodResult: map[string]any{
				"timezone": "UTC", "iso": "2026-08-31T12:00:00Z", "display": "2026-08-31 12:00:00 UTC",
			},
			InvalidCalls: []tooltest.InvalidCall{
				{Name: "missing timezone", Arguments: json.RawMessage(`{}`), WantArgumentCode: "required"},
				{Name: "wrong timezone type", Arguments: json.RawMessage(`{"timezone":8}`), WantArgumentCode: "type"},
			},
			BadResults: []tooltest.BadResult{
				{Name: "invalid timestamp", Value: map[string]any{"timezone": "UTC", "iso": "now", "display": "now"}},
				{Name: "missing display", Value: map[string]any{"timezone": "UTC", "iso": "2026-08-31T12:00:00Z"}},
			},
			ValidateResult: validateCurrentTimeResult,
		},
		"web_search": {
			ValidArguments: json.RawMessage(`{"query":"Go releases","max_results":1}`),
			GoodResult: map[string]any{
				"query": "Go releases", "trust_boundary": "untrusted_external_content", "truncated": false,
				"results": []map[string]any{{"source_id": "W1", "title": "Go releases", "url": "https://go.dev/doc/devel/release", "snippet": "Release notes", "provider_rank": 1}},
			},
			InvalidCalls: []tooltest.InvalidCall{
				{Name: "missing query", Arguments: json.RawMessage(`{}`), WantArgumentCode: "required"},
				{Name: "too many results", Arguments: json.RawMessage(`{"query":"Go","max_results":6}`), WantArgumentCode: "maximum"},
			},
			BadResults: []tooltest.BadResult{
				{Name: "missing results", Value: map[string]any{"query": "Go releases"}},
				{Name: "missing source", Value: map[string]any{"results": []map[string]any{{"title": "Go releases"}}}},
			},
			ValidateResult: validateWebSearchResult,
		},
	}
	for _, item := range catalog.List() {
		spec, ok := specs[item.Name]
		if !ok {
			t.Fatalf("default Binding %q has no contract harness case", item.Name)
		}
		binding, _ := catalog.Installed(item.Name)
		spec.Binding = binding
		t.Run(item.Name, func(t *testing.T) { tooltest.RunBindingContract(t, spec) })
		delete(specs, item.Name)
	}
	if len(specs) != 0 {
		t.Fatalf("contract cases reference non-default Bindings: %#v", specs)
	}
}

func validateWebSearchResult(value any) error {
	result, ok := value.(map[string]any)
	if !ok || result["trust_boundary"] != "untrusted_external_content" {
		return errors.New("result must mark web content as untrusted")
	}
	rows, ok := result["results"].([]map[string]any)
	if !ok || len(rows) == 0 {
		return errors.New("result requires sources")
	}
	for _, row := range rows {
		for _, field := range []string{"source_id", "title", "url", "snippet"} {
			if text, ok := row[field].(string); !ok || text == "" {
				return fmt.Errorf("source requires %s", field)
			}
		}
		if rank, ok := row["provider_rank"].(int); !ok || rank <= 0 {
			return errors.New("source requires provider rank")
		}
	}
	return nil
}

func validateCalculatorResult(value any) error {
	result, ok := value.(map[string]any)
	if !ok {
		return errors.New("result must be an object")
	}
	if expression, ok := result["expression"].(string); !ok || expression == "" {
		return errors.New("result requires expression")
	}
	number, ok := result["value"].(float64)
	if !ok || math.IsInf(number, 0) || math.IsNaN(number) {
		return errors.New("result requires a finite numeric value")
	}
	return nil
}

func validateCurrentTimeResult(value any) error {
	result, ok := value.(map[string]any)
	if !ok {
		return errors.New("result must be an object")
	}
	for _, field := range []string{"timezone", "iso", "display"} {
		if text, ok := result[field].(string); !ok || text == "" {
			return fmt.Errorf("result requires %s", field)
		}
	}
	if _, err := time.Parse(time.RFC3339, result["iso"].(string)); err != nil {
		return errors.New("result requires an RFC3339 timestamp")
	}
	return nil
}
