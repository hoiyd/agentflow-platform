package contexteval

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/inference/openai"
)

// This exercises real Compactor -> Assembly -> provider wire -> Tool Binding ->
// Turn continuation -> ledger/report. Only the remote model is deterministic.
func TestLiveCompactionContinuation(t *testing.T) {
	for _, fault := range []string{"", "generation_budget", "generation_budget_exhausted", "estimated_usage", "changed_fact", "changed_currency", "changed_payee", "negation", "canceled_revived", "uncertainty", "exact_id", "next_action", "omitted_condition", "source_fabricated", "invented_evidence", "summary_failure", "provider_error", "clipped_summary", "provider_timeout", "broken_tool", "stream_usage_fallback", "budget", "token_budget", "infeasible", "canceled", "cancel_inflight"} {
		name := fault
		if name == "" {
			name = "success"
		}
		t.Run(name, func(t *testing.T) {
			calibrated := fault == "generation_budget"
			facts, outcome := liveFixtureFacts(), liveFixtureOutcome()
			if calibrated {
				facts = strings.ReplaceAll(facts, "Approved cap is EUR", "Correction: Approved cap is EUR")
				outcome = strings.ReplaceAll(outcome, "Approved cap is EUR", "Correction: Approved cap is EUR")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var mu sync.Mutex
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requests++
				mu.Unlock()
				var body struct {
					Messages  []openai.Message  `json:"messages"`
					Stream    bool              `json:"stream"`
					Tools     []json.RawMessage `json:"tools"`
					MaxTokens int               `json:"max_tokens"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				var text strings.Builder
				for _, m := range body.Messages {
					text.WriteString(m.Content)
					text.WriteByte('\n')
				}
				all := text.String()
				if calibrated && body.MaxTokens != 8192 {
					t.Errorf("generation headroom did not reach provider: %d", body.MaxTokens)
				}
				if fault == "generation_budget_exhausted" && !body.Stream {
					_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": "incomplete"}, "finish_reason": "length"}}, "usage": map[string]int{"prompt_tokens": 100, "completion_tokens": 2048, "total_tokens": 2148}})
					return
				}
				if fault == "cancel_inflight" {
					cancel()
					<-r.Context().Done()
					return
				}
				if fault == "provider_timeout" {
					// A wire-level timeout response is deterministic under loaded CI;
					// it does not depend on a 20ms wall-clock startup race.
					http.Error(w, `{"error":{"message":"fixture timed out"}}`, http.StatusRequestTimeout)
					return
				}
				if strings.Contains(all, "Create an updated structured handoff summary") {
					if fault == "summary_failure" {
						writeLiveCompletion(w, "")
						return
					}
					if fault == "provider_error" {
						http.Error(w, `{"error":{"message":"fixture unavailable"}}`, 503)
						return
					}
					summary := facts
					if fault == "changed_fact" {
						summary = strings.ReplaceAll(summary, "45000", "99000")
					}
					mutations := map[string]struct {
						key   string
						value json.RawMessage
					}{
						"changed_currency": {"currency", json.RawMessage(`"USD"`)}, "changed_payee": {"payee", json.RawMessage(`"Account-A"`)},
						"negation": {"may_release", json.RawMessage(`true`)}, "canceled_revived": {"notice", json.RawMessage(`"pending"`)},
						"uncertainty": {"receipt", json.RawMessage(`"cleared"`)}, "exact_id": {"invoice_id", json.RawMessage(`"INV-2049"`)}, "next_action": {"action", json.RawMessage(`"release_payment"`)},
					}
					if mutation, ok := mutations[fault]; ok {
						var claims map[string]liveClaim
						_ = json.Unmarshal([]byte(summary), &claims)
						claim := claims[mutation.key]
						claim.Value = mutation.value
						claims[mutation.key] = claim
						summary = mustLiveJSON(claims)
					}
					if fault == "omitted_condition" || fault == "source_fabricated" {
						var claims map[string]liveClaim
						_ = json.Unmarshal([]byte(summary), &claims)
						if fault == "omitted_condition" {
							delete(claims, "may_release")
						} else {
							claim := claims["currency"]
							claim.SourceID = "absent"
							claims["currency"] = claim
						}
						summary = mustLiveJSON(claims)
					}
					if fault == "clipped_summary" {
						summary = strings.Repeat("irrelevant summary padding ", 2000) + summary
					}
					if fault == "estimated_usage" {
						_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": summary}, "finish_reason": "stop"}}})
					} else {
						writeLiveCompletion(w, summary)
					}
					return
				}
				if strings.Contains(all, "FIDELITY PROBE") {
					if calibrated && (!strings.Contains(all, "including introductory prefixes") || !strings.Contains(all, "unknown confirmation status")) {
						t.Error("probe instructions do not explain exact quotation and uncertainty contracts")
					}
					// Never accept a probe carrying required Task State or the truth labels.
					if strings.Contains(all, "<task_state") || strings.Contains(all, "expected_facts") {
						t.Error("probe leaked rescue state or answer key")
					}
					answer := facts
					if _, evidence, ok := strings.Cut(all, "Historical evidence:\n"); ok && json.Valid([]byte(strings.TrimSpace(evidence))) {
						answer = strings.TrimSpace(evidence)
					}
					if strings.Contains(all, "99000") {
						answer = strings.ReplaceAll(answer, "45000", "99000")
					}
					if strings.Contains(all, "summary truncated") {
						answer = `{}`
					}
					if fault == "estimated_usage" {
						_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": answer}, "finish_reason": "stop"}}})
					} else {
						writeLiveCompletion(w, answer)
					}
					return
				}
				if !body.Stream {
					t.Error("continuation must use production stream transport")
					http.Error(w, "bad", 400)
					return
				}
				if calibrated && !strings.Contains(all, "exactly one material citation") {
					t.Error("continuation instructions do not explain citation cardinality")
				}
				if fault == "stream_usage_fallback" {
					http.Error(w, `{"error":{"message":"stream_options is not supported"}}`, 400)
					return
				}
				if !strings.Contains(all, "<task_state") || !strings.Contains(all, `"status":"canceled"`) {
					t.Error("required structured state absent from real continuation request")
				}
				if strings.Contains(all, "<conversation_summary") && strings.Contains(all, "Initial cap USD 900.00") {
					t.Error("compacted source messages still present on wire")
				}
				if !strings.Contains(mustLiveJSON(body.Tools), `artifact_id`) {
					t.Error("missing scoped material Tool")
				}
				last := body.Messages[len(body.Messages)-1]
				w.Header().Set("Content-Type", "text/event-stream")
				var delta any
				finish := "stop"
				if last.Role != "tool" {
					id := liveArtifactID(all)
					arguments, _ := json.Marshal(map[string]any{"artifact_id": id, "offset": 0, "limit": 4000})
					if fault == "broken_tool" {
						delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "bad", "type": "function", "function": map[string]any{"name": "artifact_read", "arguments": "{"}}}}
					} else {
						delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "read", "type": "function", "function": map[string]any{"name": "artifact_read", "arguments": string(arguments)}}}}
					}
					finish = "tool_calls"
				} else {
					if !strings.Contains(last.Content, "Evidence: receipt is pending.") {
						t.Error("real material read did not reach continuation")
					}
					answer := outcome
					if fault == "invented_evidence" {
						answer = strings.ReplaceAll(answer, "Evidence: receipt is pending.", "Receipt has cleared.")
					}
					if strings.Contains(all, "99000") {
						answer = strings.ReplaceAll(answer, "45000", "99000")
					}
					delta = map[string]any{"content": answer}
				}
				chunk := map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}, "usage": map[string]int{"prompt_tokens": 100, "completion_tokens": 100, "total_tokens": 200}}
				if fault == "estimated_usage" {
					delete(chunk, "usage")
				}
				encoded, _ := json.Marshal(chunk)
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", encoded)
			}))
			defer server.Close()
			opts := LiveOptions{DatasetPath: "../../../../../examples/context/compaction-continuation.v1.json", Trials: 2,
				MaxModelCalls: 40, MaxTotalTokens: 100000, Timeout: time.Second, Revision: "fixture-revision", EvidenceMode: "deterministic_http_fixture"}
			if calibrated {
				opts.DatasetPath = "../../../../../examples/context/compaction-continuation.v2.json"
				opts.OutputReserveTokens = 8192
			}
			if fault == "budget" {
				opts.MaxModelCalls = 1
			}
			if fault == "stream_usage_fallback" {
				opts.MaxModelCalls = 2
			}
			if fault == "token_budget" {
				opts.MaxTotalTokens = 1
			}
			if fault == "infeasible" {
				opts.ContextWindowTokens = 1000
			}
			if fault == "clipped_summary" {
				opts.SummaryMaxTokens = 64
			}
			if fault == "canceled" {
				cancel()
			}
			client := openai.NewClient("fixture", server.URL, "fixture-model")
			report, err := RunLive(ctx, client, opts)
			if err != nil {
				t.Fatal(err)
			}
			if dir := os.Getenv("EVALUATION_REPORT_DIR"); dir != "" {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				evidence := struct {
					LiveReport
					Scenario     string `json:"fixture_scenario"`
					ExpectedGate bool   `json:"expected_gate_passed"`
					Reproduce    string `json:"reproduce_command"`
				}{report, name, fault == "" || fault == "estimated_usage" || calibrated, "go test ./internal/evaluation/contexteval -run '^TestLiveCompactionContinuation/" + name + "$' -count=1"}
				if err := os.WriteFile(filepath.Join(dir, "compaction-fixture-"+name+".json"), []byte(mustLiveJSON(evidence)), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if len(report.Samples) != 4 || report.Gate.Passed != (fault == "" || fault == "estimated_usage" || calibrated) {
				t.Fatalf("unexpected gate=%t summary=%+v", report.Gate.Passed, report.Summary)
			}
			if fault == "" || calibrated {
				if len(report.Pairs) != 2 || report.Pairs[0].Reason != "" || report.Pairs[0].CompactionCalls != 1 || report.Pairs[0].TotalTokenDelta == nil || *report.Pairs[0].TotalTokenDelta != 200 {
					t.Fatal("phase costs or controlled pairs missing")
				}
				if report.Samples[0].Arm != StrategyFullHistory || report.Samples[2].Arm != StrategyCompacted {
					t.Fatal("arm order did not rotate")
				}
				for _, s := range report.Samples {
					if len(s.MaterialReads) != 1 || s.MaterialReads[0].ContentHash != s.Artifact.ContentHash || s.MaterialReads[0].EventID == "" {
						t.Fatal("report cannot independently verify material read")
					}
					if !s.Fidelity.Passed || !s.Outcome.Passed || len(s.Ledger.Entries) == 0 || len(s.Manifests) == 0 || len(s.Events) == 0 {
						t.Fatal("missing actual continuation evidence")
					}
					if s.Arm == StrategyCompacted && (s.Compaction == nil || s.Compaction.Generation != 1 || s.SummaryHash == "") {
						t.Fatal("production summary missing")
					}
				}
				if report.Samples[0].StateHash != report.Samples[1].StateHash || report.Samples[0].TaskState.ID == report.Samples[1].TaskState.ID {
					t.Fatal("state content drift or cross-conversation revision reuse")
				}
			}
			if calibrated && (report.Assembly.OutputReserveTokens != 8192 || report.Assembly.CompactionSummaryMaxTokens != 1024 || report.Dataset.Version != "2.0.0") {
				t.Fatal("calibration configuration was not retained")
			}
			if fault == "generation_budget_exhausted" {
				for _, s := range report.Samples {
					if s.Arm == StrategyCompacted && (!s.Fallback || s.Compaction != nil || s.Reason != "incomplete_output") {
						t.Fatal("incomplete generation was accepted as a successful compaction")
					}
				}
			}
			if fault == "summary_failure" {
				observed := false
				for _, s := range report.Samples {
					if s.Arm == StrategyCompacted && s.Status != "not_evaluated" {
						observed = true
						if !s.Fallback || s.Compaction != nil || !s.Outcome.Passed {
							t.Fatalf("raw fallback missing or falsely counted as compaction: status=%s fallback=%t outcome=%v", s.Status, s.Fallback, s.Outcome)
						}
					}
				}
				if !observed {
					t.Fatal("no failed summary attempt observed")
				}
			}
			if report.Summary.Samples != 4 {
				t.Fatal("failed/skipped samples disappeared")
			}
			if report.Usage.ModelCalls > opts.MaxModelCalls {
				t.Fatal("suite call budget exceeded")
			}
			mu.Lock()
			physical := requests
			mu.Unlock()
			if report.PhysicalRequests != physical || physical > opts.MaxModelCalls {
				t.Fatalf("physical request cap/accounting invalid: report=%d observed=%d", report.PhysicalRequests, physical)
			}
			if fault == "cancel_inflight" && physical != 1 {
				t.Fatal("cancellation did not stop after first in-flight request")
			}
			if fault == "estimated_usage" {
				estimated := false
				for _, s := range report.Samples {
					for _, e := range s.Ledger.Entries {
						estimated = estimated || e.Estimated
					}
				}
				if !estimated {
					t.Fatal("missing provider usage was presented as measured")
				}
			}
			if fault == "token_budget" {
				mu.Lock()
				n := requests
				mu.Unlock()
				if n != 0 {
					t.Fatal("token reservation exceeded suite limit")
				}
			}
			if fault == "provider_error" {
				mu.Lock()
				n := requests
				mu.Unlock()
				if n != 4 {
					t.Fatalf("spent after unsettled summary: %d requests", n)
				}
			}
			if fault == "canceled" || fault == "infeasible" {
				mu.Lock()
				n := requests
				mu.Unlock()
				if n != 0 {
					t.Fatalf("preflight crossed provider: %d", n)
				}
				if report.Summary.Comparable != 0 {
					t.Fatal("infeasible or unrun pair compared")
				}
				var serialized map[string]any
				_ = json.Unmarshal([]byte(mustLiveJSON(report)), &serialized)
				if serialized["pairs"].([]any)[0].(map[string]any)["total_token_delta"] != nil {
					t.Fatal("incomparable pair invents zero cost delta")
				}
			}
			serialized := mustLiveJSON(report)
			if strings.Contains(serialized, server.URL) || strings.Contains(serialized, `"api_key"`) {
				t.Fatal("endpoint/credential leaked")
			}
		})
	}
}

func writeLiveCompletion(w http.ResponseWriter, text string) {
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": text}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 100, "completion_tokens": 100, "total_tokens": 200}})
}

func mustLiveJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func liveFixtureFacts() string {
	return `{"amount_minor":{"value":45000,"source_id":"h3","quote":"Approved cap is EUR 450.00 for Account-B, not Account-A."},"currency":{"value":"EUR","source_id":"h3","quote":"Approved cap is EUR 450.00 for Account-B, not Account-A."},"payee":{"value":"Account-B","source_id":"h3","quote":"Approved cap is EUR 450.00 for Account-B, not Account-A."},"may_release":{"value":false,"source_id":"h3","quote":"Do not release payment until a receipt is confirmed."},"notice":{"value":"canceled","source_id":"h5","quote":"Cancel the send-notice task; do not revive it."},"receipt":{"value":"unknown","source_id":"h5","quote":"Receipt confirmation is unknown, not cleared."},"invoice_id":{"value":"INV-2048","source_id":"h1","quote":"Invoice INV-2048 belongs to Case-C17."},"action":{"value":"draft_only","source_id":"h5","quote":"The next allowed action is draft_only."}}`
}

func liveFixtureOutcome() string {
	return `{"facts":` + liveFixtureFacts() + `,"citations":[{"source_id":"doc-1","quote":"Evidence: receipt is pending."}]}`
}

func liveArtifactID(text string) string {
	for _, word := range strings.Fields(text) {
		if strings.HasPrefix(word, "tool_artifact_") {
			return strings.Trim(word, "\".,;\n")
		}
	}
	return "missing"
}
