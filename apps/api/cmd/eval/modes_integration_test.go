package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"agentflow-platform/apps/api/internal/testsupport/modelstream"
)

// Failure inventory lives in the versioned task. Only the provider transport is
// stubbed: HTTP commands, Runtime, file_read, persistence and sensors are real.
func TestModeAblationUsesRealModesAndRotatesTrials(t *testing.T) {
	args, requests, _ := modeFixture(t, "good")
	var out, stderr bytes.Buffer
	if code := run(t.Context(), args, &out, &stderr); code != 0 {
		retainModeEvidence(t, "orchestration-functional-failed.json", out.Bytes())
		t.Fatalf("exit=%d error=%s", code, stderr.String())
	}
	var report struct {
		Schema  string `json:"schema_version"`
		Axis    string `json:"experiment_axis"`
		Samples []struct {
			Mode, Status   string
			Verified       bool
			RunID          string `json:"run_id"`
			ConversationID string `json:"conversation_id"`
			Approvals      []json.RawMessage
			Replay         struct {
				Steps    []struct{ Role string }
				Messages []json.RawMessage
			}
		}
		Comparisons      []struct{ Comparable bool }
		PhysicalRequests int `json:"physical_requests"`
		Gate             struct{ Passed bool }
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.Schema != "orchestration-mode-eval-v1" || report.Axis != "orchestration_mode" || len(report.Samples) != 9 || !report.Gate.Passed {
		t.Fatalf("invalid evidence: %v %s", err, out.String())
	}
	seen := map[string]bool{}
	want := []string{"single", "multi_agent", "autonomous", "multi_agent", "autonomous", "single", "autonomous", "single", "multi_agent"}
	for i, sample := range report.Samples {
		if sample.Mode != want[i] || !sample.Verified || sample.Status != "completed" || sample.RunID == "" || sample.ConversationID == "" || seen[sample.ConversationID] || len(sample.Replay.Messages) < 2 {
			t.Fatalf("unverified or shared-state arm: %+v", sample)
		}
		seen[sample.ConversationID] = true
		if sample.Mode == "multi_agent" && (len(sample.Approvals) != 1 || len(sample.Replay.Steps) != 5) {
			t.Fatal("Multi must retain its real approval and five stages")
		}
	}
	if len(report.Comparisons) != 6 || report.PhysicalRequests != int(requests.Load()) {
		t.Fatal("missing pair or physical-request evidence")
	}
	for _, comparison := range report.Comparisons {
		if !comparison.Comparable {
			t.Fatal("controlled modes should compare")
		}
	}
	retainModeEvidence(t, "orchestration-functional.json", out.Bytes())
}

func TestModeAblationFailureOutcomesAreNotHidden(t *testing.T) {
	for _, scenario := range []string{"missing_fact", "false_review", "no_progress", "no_read", "malformed", "extra_claim", "provider_error", "provider_timeout", "cancellation", "run_budget", "token_budget"} {
		t.Run(scenario, func(t *testing.T) {
			args, _, entered := modeFixture(t, scenario)
			args = append(args, "--trials", "1")
			ctx := t.Context()
			if scenario == "cancellation" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				defer cancel()
				go func() {
					select {
					case <-entered:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			if scenario == "run_budget" {
				args = append(args, "--sample-max-model-calls", "1")
			}
			if scenario == "token_budget" {
				args = append(args, "--sample-max-tokens", "1")
			}
			var out, stderr bytes.Buffer
			code := run(ctx, args, &out, &stderr)
			var report struct {
				Samples []struct {
					Verified        bool
					Findings        []string
					Status          string
					NoProgress      int  `json:"no_progress_iterations"`
					FalseAcceptance bool `json:"reviewed_but_unverified"`
					Iterations      int
					Usage           struct {
						Open int `json:"open_reservations"`
					}
				}
				Gate struct{ Passed bool }
			}
			if code == 2 || json.Unmarshal(out.Bytes(), &report) != nil || len(report.Samples) != 3 {
				t.Fatalf("failure lost its report: code=%d %s", code, stderr.String())
			}
			failed := 0
			for _, sample := range report.Samples {
				if !sample.Verified || (scenario == "no_progress" && sample.NoProgress > 0) {
					failed++
				}
				if scenario == "no_progress" && sample.Iterations > 2 {
					t.Fatal("Loop exceeded the fixed iteration bound")
				}
			}
			if failed == 0 {
				t.Fatal("injected failure was silently accepted")
			}
			if scenario == "false_review" && !report.Samples[1].FalseAcceptance {
				t.Fatal("Reviewer false acceptance must remain visible")
			}
			if scenario == "cancellation" && report.Samples[0].Status != "canceled" {
				t.Fatalf("cancellation must durably stop accepted work: %+v", report.Samples[0])
			}
			if scenario == "provider_error" && (report.Samples[0].Usage.Open == 0 || report.Samples[1].Status != "not_evaluated") {
				t.Fatal("uncertain usage must stop further suite spending")
			}
			retainModeEvidence(t, "orchestration-"+scenario+".json", out.Bytes())
		})
	}
}

func TestModeAblationApprovalAndSharedBudgetBoundaries(t *testing.T) {
	for _, policy := range []string{"edit", "reject", "budget", "tokens"} {
		t.Run(policy, func(t *testing.T) {
			args, requests, _ := modeFixture(t, "good")
			args = append(args, "--trials", "1")
			if policy == "budget" {
				args = append(args, "--max-model-requests", "1")
			} else if policy == "tokens" {
				args = append(args, "--max-total-tokens", "60000")
			} else {
				args = append(args, "--approval-policy", policy)
			}
			var out, stderr bytes.Buffer
			if code := run(t.Context(), args, &out, &stderr); code == 2 {
				t.Fatalf("no report: %s", stderr.String())
			}
			if policy == "budget" && requests.Load() > 1 {
				t.Fatal("suite physical cap multiplied across modes")
			}
			if policy == "tokens" && requests.Load() != 2 {
				t.Fatal("suite token admission must reserve the common ceiling, not shrink later arms")
			}
			var report struct {
				Samples []struct {
					Status    string
					Approvals []struct {
						Original  string `json:"original_hash"`
						Submitted string `json:"submitted_hash"`
					}
					Replay struct {
						Steps []struct{ Role, Output string }
					}
				}
			}
			if json.Unmarshal(out.Bytes(), &report) != nil || len(report.Samples) != 3 {
				t.Fatal("boundary lost planned arms")
			}
			multi := report.Samples[1]
			if policy == "reject" && (multi.Status != "canceled" || len(multi.Replay.Steps) != 1) {
				t.Fatal("rejected plan must never run a Worker")
			}
			if policy == "edit" {
				a := multi.Approvals
				if len(a) != 1 || a[0].Original == "" || a[0].Submitted == a[0].Original {
					t.Fatal("edited plan hashes must differ")
				}
				if !strings.Contains(multi.Replay.Steps[0].Output, "Preserve the exact source quotation") {
					t.Fatal("edit was not persisted through Continue")
				}
			}
			if !strings.Contains(out.String(), `"`+map[string]string{"edit": "edit", "reject": "reject", "budget": "not_evaluated", "tokens": "not_evaluated"}[policy]+`"`) {
				t.Fatalf("boundary action missing: %s", out.String())
			}
			retainModeEvidence(t, "orchestration-"+policy+".json", out.Bytes())
		})
	}
}

func TestModeAblationRejectsMissingConsentAndLimits(t *testing.T) {
	for _, args := range [][]string{{"modes"}, {"modes", "--live"}, {"modes", "--unknown"}} {
		if code := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}); code != 2 {
			t.Fatalf("invalid command accepted: %v", args)
		}
	}
}

// These checks must reject untrusted configuration before contacting Postgres
// or a model. Dataset errors must not echo its contents or credential values.
func TestModeAblationValidatesInputsBeforeExecution(t *testing.T) {
	t.Setenv("MODE_EVAL_VALIDATION_KEY", "private-fixture-value")
	valid := []string{"modes", "--live", "--dataset", "../../../../examples/orchestration/report-task.v1.json", "--database-url", "postgres://localhost/production", "--model", "fixture", "--base-url", "http://127.0.0.1:1/v1", "--credential-env", "MODE_EVAL_VALIDATION_KEY", "--max-model-requests", "1", "--max-total-tokens", "10", "--sample-max-model-calls", "1", "--sample-max-tokens", "10"}
	for _, bad := range [][]string{
		{}, {"--base-url", "https://secret@host/v1"}, {"--base-url", "https://host/v1?key=secret"}, {"--model", "bad\nmodel"},
		{"--credential-env", "ABSENT_MODE_EVAL_KEY"}, {"--trials", "0"}, {"--sample-max-tokens", "11"}, {"--approval-policy", "bypass"}, {"--evidence-mode", "unknown"},
		{"--dataset", filepath.Join(t.TempDir(), "missing.json")},
	} {
		var out, stderr bytes.Buffer
		args := append(append([]string{}, valid...), bad...)
		if code := run(t.Context(), args, &out, &stderr); code != 2 || out.Len() != 0 || strings.Contains(stderr.String(), "private-fixture-value") {
			t.Fatalf("unsafe config accepted/leaked: code=%d %s", code, stderr.String())
		}
	}
	for name, content := range map[string]string{
		"oversized": strings.Repeat("x", 32769), "malformed": "{", "unknown": `{"extra":true}`, "trailing": `{} {}`,
		"missing_fields": `{"schema_version":"orchestration-task-v1","id":"x","version":"1","task":"x","material":"x","approval_edit":"x","failure_inventory":["1","2","3","4","5","6","7"],"expected":{"record":1,"region":1,"cap":1,"state":1,"source":"brief.md","other":1}}`,
		"invalid_quote":  `{"schema_version":"orchestration-task-v1","id":"x","version":"1","task":"x","material":"x","approval_edit":"x","failure_inventory":["1","2","3","4","5","6","7"],"expected":{"record":1,"region":1,"cap":1,"state":1,"source":"brief.md","quote":"not in material"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "task.json")
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			var out, stderr bytes.Buffer
			if code := run(t.Context(), append(append([]string{}, valid...), "--dataset", path), &out, &stderr); code != 2 || out.Len() != 0 {
				t.Fatalf("invalid dataset accepted: %d %s", code, stderr.String())
			}
		})
	}
}

func modeFixture(t *testing.T, scenario string) ([]string, *atomic.Int32, <-chan struct{}) {
	t.Helper()
	db := os.Getenv("TEST_DATABASE_URL")
	if db == "" {
		t.Skip("requires dedicated Postgres; the evaluator creates/drops its own database")
	}
	t.Setenv("MODE_EVAL_FIXTURE_KEY", "fixture-only")
	var requests atomic.Int32
	entered := make(chan struct{})
	var once sync.Once
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		once.Do(func() { close(entered) })
		var input struct {
			Stream   bool
			Messages []struct{ Role, Content string }
			Tools    []struct{ Function struct{ Name string } }
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || len(input.Messages) == 0 {
			t.Error("invalid model wire request")
			http.Error(w, "bad request", 400)
			return
		}
		if scenario == "provider_error" {
			http.Error(w, "controlled provider outage", 503)
			return
		}
		if scenario == "provider_timeout" {
			http.Error(w, "controlled interrupted provider", 504)
			return
		}
		if scenario == "cancellation" {
			<-r.Context().Done()
			return
		}
		output := `{"record":"QX-17","region":"eu-north-1","cap":64,"state":"held","source":"brief.md","quote":"The current request cap is 64."}`
		if scenario == "missing_fact" {
			output = `{"record":"QX-17","source":"brief.md"}`
		}
		if scenario == "extra_claim" {
			output = strings.TrimSuffix(output, "}") + `,"other_state":"released"}`
		}
		system := input.Messages[0].Content
		if scenario == "malformed" {
			output = `{"cap":{"nested":"invalid"}}`
		}
		if scenario == "false_review" && (strings.Contains(system, "Worker collaboration") || strings.Contains(system, "Finalizer")) {
			output = `{"record":"QX-17","region":"eu-north-1","cap":32,"state":"released","source":"brief.md","quote":"The current request cap is 64."}`
		}
		reason := "stop"
		var calls []any
		canRead, didRead := false, false
		for _, definition := range input.Tools {
			canRead = canRead || definition.Function.Name == "file_read"
		}
		for _, message := range input.Messages {
			if message.Role == "tool" && strings.Contains(message.Content, "current request cap") {
				didRead = true
			}
		}
		if canRead && !didRead && scenario != "no_read" {
			output, reason = "", "tool_calls"
			calls = []any{map[string]any{"id": "read-brief", "type": "function", "function": map[string]string{"name": "file_read", "arguments": `{"root":"source","path":"brief.md"}`}}}
		} else if strings.Contains(system, "Decide stage") {
			decision := "stop"
			if scenario == "no_progress" {
				decision = "continue"
			}
			body, _ := json.Marshal(map[string]string{"decision": decision, "reason": "source checked", "question": "", "final_answer": output})
			output = string(body)
		} else if scenario == "false_review" && strings.Contains(system, "Reviewer") {
			output = "Approved. All requested facts are correct."
		} else if strings.Contains(system, "Planner collaboration") || strings.Contains(system, "Plan stage") {
			output = "Read source/brief.md; report current facts and exact quote."
		}
		completion := map[string]any{"model": "fixture-model", "choices": []any{map[string]any{"finish_reason": reason, "message": map[string]any{"role": "assistant", "content": output, "tool_calls": calls}}}, "usage": map[string]int{"prompt_tokens": 100, "completion_tokens": 20, "total_tokens": 120}}
		if input.Stream {
			data, err := modelstream.Completion(completion)
			if err != nil {
				t.Error(err)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(data))
		} else {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(completion)
		}
	}))
	t.Cleanup(provider.Close)
	return []string{"modes", "--live", "--evidence-mode", "deterministic_http_fixture", "--database-url", db,
		"--dataset", filepath.Join("..", "..", "..", "..", "examples", "orchestration", "report-task.v1.json"),
		"--model", "fixture-model", "--base-url", provider.URL + "/v1", "--credential-env", "MODE_EVAL_FIXTURE_KEY",
		"--trials", "3", "--max-model-requests", "120", "--max-total-tokens", "1000000", "--sample-max-model-calls", "16", "--sample-max-tokens", "60000",
		"--timeout", "20s", "--suite-timeout", "2m", "--enforce"}, &requests, entered
}

func retainModeEvidence(t *testing.T, name string, content []byte) {
	t.Helper()
	if directory := os.Getenv("EVALUATION_REPORT_DIR"); directory != "" {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, name), content, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
