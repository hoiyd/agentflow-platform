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
	"sync/atomic"
	"testing"

	"agentflow-platform/apps/api/internal/evaluation/tooleval"
	"agentflow-platform/apps/api/internal/testsupport/modelstream"
)

// The versioned task lists failure modes before implementation. Only model
// transport is stubbed: Skill loading, filesystem writes, journal and Replay
// must use the production composition with a disposable database.
func TestFileSkillTaskRealOutcomeAndRollout(t *testing.T) {
	for _, scenario := range []string{"good", "gain", "rotation", "wrong_fact", "no_write", "no_readback", "no_method", "false_activation", "explicit_only", "provider_error", "correction", "unsafe_write", "extra_file", "estimated_usage", "budget", "token_budget", "request_cost", "token_cost", "cancellation", "timeout"} {
		t.Run(scenario, func(t *testing.T) {
			args, requests, entered := fileSkillFixture(t, scenario)
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
			var out, stderr bytes.Buffer
			code := run(ctx, args, &out, &stderr)
			if dir := os.Getenv("EVALUATION_REPORT_DIR"); dir != "" {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "file-skill-"+scenario+".json"), out.Bytes(), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var report tooleval.FileSkillReport
			wantSamples := 4
			if scenario == "rotation" {
				wantSamples = 8
			}
			if code == 2 || json.Unmarshal(out.Bytes(), &report) != nil || report.SchemaVersion != "file-skill-eval-v1" || len(report.Samples) != wantSamples {
				t.Fatalf("no task evidence: code=%d %s", code, stderr.String())
			}
			if scenario == "good" || scenario == "correction" || scenario == "gain" || scenario == "rotation" || scenario == "estimated_usage" {
				if code != 0 || !report.Gate.Passed {
					t.Fatalf("valid outcome rejected: %+v", report.Gate)
				}
				seen := map[string]bool{}
				for _, s := range report.Samples {
					if (!s.Verified && !(scenario == "gain" && s.Arm == "without_skill" && s.TaskID == "sourced-report")) || s.RunID == "" || seen[s.RunID] {
						t.Fatal("shared or unverified Run")
					}
					seen[s.RunID] = true
					if s.FileContent != "" && (len(s.Replay.ToolEffects) == 0 || s.Verified && !strings.Contains(s.FileContent, "Cap: 64")) {
						t.Fatal("missing real output/journal")
					}
				}
			} else if report.Gate.Passed || code != 1 {
				t.Fatalf("degraded/unsafe candidate passed rollout: %+v", report.Gate)
			}
			if report.PhysicalRequests != int(requests.Load()) {
				t.Fatal("model attempts not shared/accounted")
			}
			if scenario == "budget" && requests.Load() > 1 || scenario == "token_budget" && requests.Load() > 4 {
				t.Fatal("suite limit multiplied per arm")
			}
			if scenario == "gain" && (report.Comparisons[0].OutcomeDelta == nil || *report.Comparisons[0].OutcomeDelta != 1) {
				t.Fatal("quality gain not measured")
			}
			if scenario == "rotation" && (report.Samples[4].Arm != "with_skill" || report.Samples[5].Arm != "without_skill") {
				t.Fatal("trial order was not rotated")
			}
			if scenario == "timeout" || scenario == "cancellation" {
				select {
				case <-entered:
				default:
					t.Fatal("did not exercise an in-flight model request")
				}
			}
			if report.Gate.BlockingFailures > report.Gate.BlockingSamples {
				t.Fatal("failed sample counted twice")
			}
			if scenario == "correction" && len(report.Samples[0].ToolFailures) == 0 {
				t.Fatal("argument correction evidence missing")
			}
			if scenario == "estimated_usage" && (!report.Samples[0].UsageEstimated || report.TotalTokens == 0) {
				t.Fatal("missing estimated-usage provenance")
			}
			if scenario == "extra_file" && len(report.Samples[0].SafetyFailures) == 0 {
				t.Fatal("unexpected output was not a safety failure")
			}
			for _, s := range report.Samples {
				if s.Usage.CostUnknownEntries > 0 && s.CostMicros != nil {
					t.Fatal("unknown pricing represented as free")
				}
			}
		})
	}
}

func fileSkillFixture(t *testing.T, scenario string) ([]string, *atomic.Int32, <-chan struct{}) {
	t.Helper()
	database := os.Getenv("TEST_DATABASE_URL")
	if database == "" {
		t.Skip("requires disposable Postgres with CREATEDB")
	}
	t.Setenv("FILE_SKILL_FIXTURE_KEY", "fixture-only")
	var requests atomic.Int32
	entered := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			close(entered)
		}
		var input struct {
			Messages []struct {
				Role, Content string
				ToolCalls     []struct{ Function struct{ Name string } } `json:"tool_calls"`
			}
			Tools []struct{ Function struct{ Name string } }
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			t.Error("bad model request")
			http.Error(w, "invalid", 400)
			return
		}
		if scenario == "provider_error" {
			http.Error(w, "controlled outage", 503)
			return
		}
		if scenario == "cancellation" || scenario == "timeout" {
			<-r.Context().Done()
			return
		}
		history := ""
		completed := map[string]int{}
		bound := false
		for _, m := range input.Messages {
			history += m.Content + "\n"
			for _, c := range m.ToolCalls {
				completed[c.Function.Name]++
			}
			bound = bound || strings.Contains(m.Content, `"name":"release-report"`) && strings.Contains(m.Content, "<available_skill>")
		}
		neighbor := strings.Contains(history, "This is an acknowledgment")
		name, args, answer := "", "", "SAVED"
		if neighbor {
			answer = "READY"
		}
		method := bound && (!neighbor || scenario == "false_activation") && scenario != "no_method"
		switch {
		case method && completed["skill_load"] == 0:
			name, args = "skill_load", `{"name":"release-report"}`
		case method && completed["skill_read"] == 0:
			if !strings.Contains(history, "Release-report method:") {
				t.Error("instructions did not reach the next real model request")
			}
			name, args = "skill_read", `{"name":"release-report","path":"references/checklist.md"}`
		case neighbor:
		case completed["file_read"] == 0:
			if method && !strings.Contains(history, "Prefer current facts") {
				t.Error("resource did not reach the request")
			}
			name, args = "file_read", `{"root":"source","path":"brief.md"}`
		case scenario == "unsafe_write" && completed["file_write"] == 0:
			name, args = "file_write", `{"root":"source","path":"escape.md","content":"must not publish"}`
		case scenario == "correction" && completed["file_write"] == 0:
			name, args = "file_write", `{"root":"output","path":"report.md"}`
		case scenario != "no_write" && (completed["file_write"] == 0 || scenario == "correction" && completed["file_write"] == 1):
			content := "# Release check\nRecord: QX-17\nRegion: eu-north-1\nCap: 64\nState: held\nSource: brief.md\nQuote: The current request cap is 64.\n"
			if scenario == "wrong_fact" && bound || scenario == "gain" && !bound {
				content = strings.ReplaceAll(content, "Cap: 64", "Cap: 32")
			}
			body, _ := json.Marshal(map[string]string{"root": "output", "path": "report.md", "content": content})
			name, args = "file_write", string(body)
		case scenario == "extra_file" && completed["file_write"] == 1:
			name, args = "file_write", `{"root":"output","path":"other.md","content":"unexpected"}`
		case scenario != "no_write" && scenario != "no_readback" && completed["file_read"] == 1:
			name, args = "file_read", `{"root":"output","path":"report.md"}`
		}
		var calls []any
		reason := "stop"
		if name != "" {
			answer = ""
			reason = "tool_calls"
			calls = []any{map[string]any{"id": "fixture-call", "type": "function", "function": map[string]string{"name": name, "arguments": args}}}
		}
		response := map[string]any{"model": "fixture-model", "choices": []any{map[string]any{"finish_reason": reason, "message": map[string]any{"role": "assistant", "content": answer, "tool_calls": calls}}}, "usage": map[string]int{"prompt_tokens": 100, "completion_tokens": 20, "total_tokens": 120}}
		if scenario == "estimated_usage" {
			delete(response, "usage")
		}
		stream, err := modelstream.Completion(response)
		if err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(stream))
	}))
	t.Cleanup(server.Close)
	args := []string{"tool", "--profile", "file_skill", "--live", "--base-url", server.URL + "/v1", "--model", "fixture-model", "--credential-env", "FILE_SKILL_FIXTURE_KEY", "--database-url", database,
		"--dataset", "../../../../examples/tools/file-skill-v1/dataset.json", "--skill-dir", "../../../../examples/tools/file-skill-v1/skills", "--evidence-mode", "deterministic_http_fixture",
		"--trials", "1", "--max-model-calls", "100", "--max-total-tokens", "500000", "--sample-max-model-calls", "12", "--sample-max-tokens", "60000", "--timeout", "20s", "--suite-timeout", "2m", "--enforce"}
	if scenario == "budget" {
		args = append(args, "--max-model-calls", "1")
	}
	if scenario == "token_budget" {
		args = append(args, "--max-total-tokens", "60000")
	}
	if scenario == "rotation" {
		args = append(args, "--trials", "2")
	}
	if scenario == "request_cost" {
		args = append(args, "--max-extra-model-requests", "1")
	}
	if scenario == "token_cost" {
		args = append(args, "--max-token-ratio", "1.1")
	}
	if scenario == "timeout" {
		args = append(args, "--timeout", "2s")
	}
	if scenario == "estimated_usage" {
		args = append(args, "--max-token-ratio", "10")
	}
	if scenario == "explicit_only" {
		root := t.TempDir()
		pkg := filepath.Join(root, "release-report")
		if err := os.MkdirAll(filepath.Join(pkg, "references"), 0700); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"SKILL.md", "references/checklist.md"} {
			b, err := os.ReadFile(filepath.Join("../../../../examples/tools/file-skill-v1/skills/release-report", name))
			if err != nil {
				t.Fatal(err)
			}
			if name == "SKILL.md" {
				b = bytes.Replace(b, []byte("name: release-report"), []byte("name: release-report\ndisable-model-invocation: true"), 1)
			}
			if err = os.WriteFile(filepath.Join(pkg, name), b, 0600); err != nil {
				t.Fatal(err)
			}
		}
		args = append(args, "--skill-dir", root)
	}
	return args, &requests, entered
}
