package provider

import (
	"encoding/json"
	"testing"
)

// Failure inventory: missing/null is unknown, explicit zero is known, subsets
// never inflate totals, and malformed/conflicting details cannot earn discounts.
func TestUsageBreakdownWireContract(t *testing.T) {
	for _, tc := range []struct {
		name, extra, source string
		cached, reasoning   any
	}{
		{"missing", "", "", nil, nil},
		{"undocumented normalized field", `,"breakdown":{"source":"openai_details","cached_input_tokens":6}`, "", nil, nil},
		{"irrelevant field type", `,"breakdown":"unsupported"`, "", nil, nil},
		{"malformed object", `,"prompt_tokens_details":[]`, "invalid_details", nil, nil},
		{"miss without hit", `,"prompt_cache_miss_tokens":4`, "invalid_details", nil, nil},
		{"hit only", `,"prompt_cache_hit_tokens":10`, "deepseek_cache", float64(10), nil},
		{"reasoning only", `,"completion_tokens_details":{"reasoning_tokens":0}`, "openai_details", nil, float64(0)},
		{"null subsets", `,"prompt_tokens_details":{"cached_tokens":null}`, "", nil, nil},
		{"null", `,"prompt_tokens_details":null`, "", nil, nil},
		{"reported zero", `,"prompt_tokens_details":{"cached_tokens":0}`, "openai_details", float64(0), nil},
		{"both subsets", `,"prompt_tokens_details":{"cached_tokens":6},"completion_tokens_details":{"reasoning_tokens":2}`, "openai_details", float64(6), float64(2)},
		{"deepseek", `,"prompt_cache_hit_tokens":6,"prompt_cache_miss_tokens":4`, "deepseek_cache", float64(6), nil},
		{"negative", `,"prompt_tokens_details":{"cached_tokens":-1}`, "invalid_details", nil, nil},
		{"too large", `,"prompt_tokens_details":{"cached_tokens":11}`, "invalid_details", nil, nil},
		{"wrong type", `,"prompt_tokens_details":{"cached_tokens":"6"}`, "invalid_details", nil, nil},
		{"reasoning overflow", `,"completion_tokens_details":{"reasoning_tokens":5}`, "invalid_details", nil, nil},
		{"conflicting aliases", `,"prompt_tokens_details":{"cached_tokens":5},"prompt_cache_hit_tokens":6`, "invalid_details", nil, nil},
		{"inconsistent misses", `,"prompt_cache_hit_tokens":6,"prompt_cache_miss_tokens":3`, "invalid_details", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var usage Usage
			if err := json.Unmarshal([]byte(`{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14`+tc.extra+`}`), &usage); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(usage)
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if err := json.Unmarshal(encoded, &wire); err != nil {
				t.Fatal(err)
			}
			if usage.PromptTokens != 10 || usage.CompletionTokens != 4 || usage.TotalTokens != 14 {
				t.Fatalf("subsets changed totals: %s", encoded)
			}
			b, _ := wire["breakdown"].(map[string]any)
			if tc.source == "" {
				if b != nil {
					t.Fatalf("unknown became reported: %s", encoded)
				}
				return
			}
			if b["source"] != tc.source || b["cached_input_tokens"] != tc.cached || b["reasoning_tokens"] != tc.reasoning {
				t.Fatalf("breakdown=%s", encoded)
			}
		})
	}
}
