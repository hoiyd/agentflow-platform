package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/modelstream"
)

func TestKnowledgeToolRejectsUnapprovedOrMislabelledProfiles(t *testing.T) {
	t.Setenv("KNOWLEDGE_FIXTURE_KEY", "fixture-only")
	for _, tc := range []struct {
		extra []string
		want  string
	}{
		{nil, "requires --live"},
		{[]string{"--live", "--skill-dir", "/ignored"}, "does not accept file/Skill"},
		{[]string{"--live", "--evidence-mode", "deterministic_http_fixture", "--base-url", "https://example.invalid/v1"}, "requires a loopback IP"},
		{[]string{"--live", "--base-url", "file:///not-a-model"}, "invalid Knowledge evaluation model target"},
		{[]string{"--live", "--evidence-mode", "unknown"}, "invalid Knowledge evaluation evidence mode"},
		{[]string{"--live", "--max-model-calls", "0"}, "invalid Knowledge evaluation suite or per-Run budget"},
		{[]string{"--live"}, "require a dedicated Postgres test database"},
	} {
		args := append([]string{"tool", "--profile", "knowledge", "--model", "fixture", "--database-url", "not-used",
			"--credential-env", "KNOWLEDGE_FIXTURE_KEY", "--max-model-calls", "6", "--max-total-tokens", "120000"}, tc.extra...)
		var out, stderr bytes.Buffer
		if code := run(t.Context(), args, &out, &stderr); code != 2 || out.Len() != 0 ||
			!strings.Contains(stderr.String(), tc.want) || strings.Contains(stderr.String(), "not-used") {
			t.Fatalf("invalid invocation was not rejected safely: exit=%d %s", code, stderr.String())
		}
	}
}

// Only the model is scripted. Postgres, ingestion, Tools, citations, and Replay
// are real. Incorrect selection, unsupported claims, and missing samples block.
func TestKnowledgeToolTaskRollout(t *testing.T) {
	database := os.Getenv("TEST_DATABASE_URL")
	if database == "" {
		t.Skip("requires disposable Postgres with CREATEDB")
	}
	t.Setenv("KNOWLEDGE_FIXTURE_KEY", "fixture-only")
	for _, scenario := range []string{"good", "no_read", "failed_read", "forged_citation", "unnecessary_search", "claim_on_miss", "wrong_miss_query", "budget", "admission_budget"} {
		t.Run(scenario, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				knowledgeFixtureResponse(t, w, r, scenario)
			}))
			t.Cleanup(server.Close)
			args := []string{"tool", "--profile", "knowledge", "--live", "--model", "fixture-model", "--base-url", server.URL + "/v1",
				"--credential-env", "KNOWLEDGE_FIXTURE_KEY", "--database-url", database, "--evidence-mode", "deterministic_http_fixture",
				"--max-model-calls", "12", "--max-total-tokens", "60000", "--sample-max-tokens", "12000", "--timeout", "15s", "--enforce"}
			if scenario == "budget" {
				args = append(args, "--max-model-calls", "1")
			} else if scenario == "admission_budget" {
				args = append(args, "--max-total-tokens", "12000")
			}
			var out, stderr bytes.Buffer
			code := run(t.Context(), args, &out, &stderr)
			var report struct {
				PhysicalRequests int      `json:"physical_requests"`
				TotalTokens      int      `json:"total_tokens"`
				CostPerSuccess   *float64 `json:"cost_per_verified_success_micros"`
				Gate             struct{ Passed bool }
				Samples          []struct {
					RunID          string `json:"run_id"`
					Status         string
					Verified       bool
					Findings       []string
					UsageEstimated bool                `json:"usage_estimated"`
					CitedSource    *domain.RAGCitation `json:"cited_source"`
				}
			}
			if json.Unmarshal(out.Bytes(), &report) != nil || len(report.Samples) != 3 {
				t.Fatalf("expected all three samples in report, exit=%d: %s", code, stderr.String())
			}
			if code == 2 || report.Gate.Passed != (scenario == "good") || (code == 0) != report.Gate.Passed {
				t.Fatalf("incorrect rollout decision: exit=%d %s, samples=%+v", code, stderr.String(), report.Samples)
			}
			if report.PhysicalRequests != int(requests.Load()) || report.CostPerSuccess != nil ||
				(scenario != "budget" && report.TotalTokens != report.PhysicalRequests*120) {
				t.Fatalf("incorrect task cost/usage accounting: %+v", report)
			}
			if scenario == "good" {
				first := report.Samples[0]
				if first.CitedSource == nil || first.CitedSource.RunID != first.RunID || first.CitedSource.ToolEventID == "" || first.CitedSource.ToolCallID == "" ||
					report.PhysicalRequests != 6 || !report.Samples[1].Verified || !report.Samples[2].Verified {
					t.Fatalf("missing persisted source or task checks: %+v", report.Samples)
				}
			}
			if scenario == "budget" && (requests.Load() != 1 || report.TotalTokens < 120 || !report.Samples[0].UsageEstimated ||
				report.Samples[1].Status != "not_evaluated" || report.Samples[2].Status != "not_evaluated") {
				t.Fatalf("budget was exceeded or omitted planned samples: %+v", report)
			}
			switch scenario {
			case "no_read":
				if !slices.Contains(report.Samples[0].Findings, "missing_ordered_read_evidence") {
					t.Fatal("missing read did not trigger evidence failure")
				}
			case "failed_read":
				if !slices.Contains(report.Samples[0].Findings, "tool_failed") || !slices.Contains(report.Samples[0].Findings, "unsupported_citation") {
					t.Fatal("failed read counted as evidence or did not surface Tool failure")
				}
			case "forged_citation":
				if !slices.Contains(report.Samples[0].Findings, "unsupported_citation") {
					t.Fatal("forged source did not trigger citation failure")
				}
			case "unnecessary_search":
				if !slices.Contains(report.Samples[1].Findings, "unexpected_tool") {
					t.Fatal("unexpected tool use did not trigger a selection failure")
				}
			case "claim_on_miss":
				if !slices.Contains(report.Samples[2].Findings, "incorrect_final_reply") {
					t.Fatal("unsupported claim after no-match passed")
				}
			case "wrong_miss_query":
				if !slices.Contains(report.Samples[2].Findings, "missing_no_match_evidence") {
					t.Fatal("unrelated no-match counted as evidence")
				}
			case "admission_budget":
				if !report.Samples[0].Verified || report.PhysicalRequests != 3 ||
					!slices.Contains(report.Samples[1].Findings, "suite_budget_exhausted") || report.Samples[2].Status != "not_evaluated" {
					t.Fatalf("evaluator started a new sample without its reserved budget: %+v", report)
				}
			}
			if dir := os.Getenv("EVALUATION_REPORT_DIR"); dir != "" {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "knowledge-tools-"+scenario+".json"), out.Bytes(), 0600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func knowledgeFixtureResponse(t *testing.T, w http.ResponseWriter, r *http.Request, scenario string) {
	var input struct {
		Messages []struct{ Role, Content string }
		Stream   bool
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		t.Error(err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var task string
	var observations []string
	for _, m := range input.Messages {
		switch m.Role {
		case "user":
			task = m.Content
		case "tool":
			observations = append(observations, m.Content)
		}
	}
	search, answer := "HARBOUR-742", "The transfer limit is 37 crates [S1]."
	switch {
	case strings.Contains(task, "MIRAGE-913"):
		search, answer = "MIRAGE-913", "NOT FOUND"
		if scenario == "claim_on_miss" {
			answer = "The transfer limit is 37 crates [S1]."
		}
		if scenario == "wrong_miss_query" {
			search = "WRONG-123"
		}
	case strings.Contains(task, "READY"):
		search, answer = "", "READY"
		if scenario == "unnecessary_search" {
			search = "HARBOUR-742"
		}
	}
	name, arguments := "", ""
	if search != "" && len(observations) == 0 {
		name, arguments = "knowledge_search", fmt.Sprintf(`{"query":%q}`, search)
	} else if len(observations) == 1 && search == "HARBOUR-742" && !strings.Contains(task, "READY") && scenario != "no_read" {
		var result struct {
			Result domain.KnowledgeToolSearchResult
		}
		if json.Unmarshal([]byte(observations[0]), &result) != nil || len(result.Result.Items) != 1 {
			t.Error("expected one real Knowledge result")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		reference := result.Result.Items[0].Reference
		if scenario == "failed_read" {
			reference = "not-issued"
		}
		name, arguments = "knowledge_read", fmt.Sprintf(`{"reference":%q}`, reference)
	} else if len(observations) == 2 && search == "HARBOUR-742" && scenario != "failed_read" {
		var result struct {
			Result domain.KnowledgeToolReadResult `json:"result"`
		}
		if json.Unmarshal([]byte(observations[1]), &result) != nil ||
			!strings.Contains(result.Result.Content, "The verified transfer limit is 37 crates.") || result.Result.Source.SourceID == "" {
			t.Error("read result did not reach the real model continuation")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		answer = fmt.Sprintf("The transfer limit is 37 crates [%s].", result.Result.Source.SourceID)
	}
	if len(observations) == 1 && strings.Contains(task, "MIRAGE-913") {
		var result struct {
			Result domain.KnowledgeToolSearchResult `json:"result"`
		}
		if json.Unmarshal([]byte(observations[0]), &result) != nil || !result.Result.NoMatch || len(result.Result.Items) != 0 {
			t.Error("model continuation did not receive the no-match result")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	}
	if scenario == "forged_citation" {
		answer = strings.ReplaceAll(answer, "[S1]", "[S99]")
	}
	reason := "stop"
	msg := map[string]any{"role": "assistant", "content": answer}
	if name != "" {
		reason, msg["content"] = "tool_calls", ""
		msg["tool_calls"] = []any{map[string]any{"id": "call-" + name, "type": "function", "function": map[string]string{"name": name, "arguments": arguments}}}
	}
	body := map[string]any{"model": "fixture-model", "choices": []any{map[string]any{"finish_reason": reason, "message": msg}},
		"usage": map[string]int{"prompt_tokens": 100, "completion_tokens": 20, "total_tokens": 120}}
	if !input.Stream {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
		return
	}
	stream, err := modelstream.Completion(body)
	if err != nil {
		t.Error(err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprint(w, stream)
}
