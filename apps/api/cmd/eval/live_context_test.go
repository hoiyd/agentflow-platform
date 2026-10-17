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
	"testing"

	"agentflow-platform/apps/api/internal/evaluation/contexteval"
)

func TestLiveContextCLIRequiresConsentAndRetainsFailures(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "fixture-key")
	var out, stderr bytes.Buffer
	if code := run(context.Background(), []string{"context-live"}, &out, &stderr); code != 2 || !strings.Contains(stderr.String(), "--live") || out.Len() != 0 {
		t.Fatalf("missing consent check: %d %s", code, stderr.String())
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"fixture failure"}}`, 503)
	}))
	defer server.Close()
	args := []string{"context-live", "--live", "--model", "fixture", "--base-url", server.URL, "--dataset", "../../../../examples/context/compaction-continuation.v1.json", "--trials", "2", "--max-model-calls", "1", "--max-total-tokens", "40000", "--output-reserve-tokens", "8192"}
	for _, enforce := range []bool{false, true} {
		out.Reset()
		stderr.Reset()
		a := append([]string{}, args...)
		want := 0
		if enforce {
			a = append(a, "--enforce")
			want = 1
		}
		if code := run(context.Background(), a, &out, &stderr); code != want {
			t.Fatalf("exit=%d stderr=%s", code, stderr.String())
		}
		var report contexteval.LiveReport
		if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.Gate.Passed || len(report.Samples) != 4 || report.Config.EvidenceMode != "live_model" {
			t.Fatalf("missing retained failure report: %v", err)
		}
		if report.Assembly.OutputReserveTokens != 8192 || report.Assembly.CompactionSummaryMaxTokens != 1024 {
			t.Fatal("generation headroom and stored-summary cap were not independently retained")
		}
		if strings.Contains(out.String(), "fixture-key") || strings.Contains(out.String(), server.URL) {
			t.Fatal("credential/endpoint leaked")
		}
	}
	if code := run(context.Background(), args, failWriter{}, &bytes.Buffer{}); code != 2 {
		t.Fatal("write failure ignored")
	}
	for _, extra := range [][]string{{"--dataset", "missing"}, {"--trials", "0"}, {"--trials", "21"}, {"--timeout", "0s"}, {"--max-model-calls", "0"}, {"--max-total-tokens", "0"}, {"--context-window-tokens", "-1"}, {"--output-reserve-tokens", "0"}, {"--output-reserve-tokens", "-1"}, {"--output-reserve-tokens", "32768"}, {"unexpected"}} {
		if code := run(context.Background(), append(append([]string{}, args...), extra...), &bytes.Buffer{}, &bytes.Buffer{}); code != 2 {
			t.Fatalf("invalid options accepted: %v", extra)
		}
	}
	t.Setenv("OPENAI_API_KEY", "")
	if code := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}); code != 2 {
		t.Fatal("missing credential accepted")
	}
}

func TestLiveContextCLIRejectsInvalidDatasetBeforeNetwork(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "fixture-key")
	content, err := os.ReadFile("../../../../examples/context/compaction-continuation.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	mutations := []struct {
		name   string
		change func(map[string]any)
	}{
		{"schema", func(d map[string]any) { d["schema_version"] = "unknown" }},
		{"private_dataset", func(d map[string]any) { d["synthetic"] = false }},
		{"extra", func(d map[string]any) { d["unexpected"] = true }},
		{"orphan_tool", func(d map[string]any) {
			d["cases"].([]any)[0].(map[string]any)["history"].([]any)[1].(map[string]any)["role"] = "tool"
		}},
		{"assistant_state_source", func(d map[string]any) { d["cases"].([]any)[0].(map[string]any)["state_source"] = "h2" }},
		{"missing_fact_evidence", func(d map[string]any) {
			d["cases"].([]any)[0].(map[string]any)["expected_facts"].(map[string]any)["currency"].(map[string]any)["source_id"] = "absent"
		}},
		{"bad_state_patch", func(d map[string]any) {
			d["cases"].([]any)[0].(map[string]any)["state_patch"].(map[string]any)["expected_version"] = 3
		}},
		{"missing_material", func(d map[string]any) {
			d["cases"].([]any)[0].(map[string]any)["material"].(map[string]any)["required_quote"] = "not in the material"
		}},
	}
	for _, m := range mutations {
		t.Run(m.name, func(t *testing.T) {
			var d map[string]any
			if err := json.Unmarshal(content, &d); err != nil {
				t.Fatal(err)
			}
			m.change(d)
			data, _ := json.Marshal(d)
			path := filepath.Join(t.TempDir(), "dataset.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			// An unreachable endpoint also makes an accidental request fail, but
			// invalid datasets must produce a configuration error, not a model report.
			args := []string{"context-live", "--live", "--model", "fixture", "--base-url", "http://127.0.0.1:1", "--dataset", path, "--max-model-calls", "1", "--max-total-tokens", "40000"}
			var out, stderr bytes.Buffer
			if code := run(context.Background(), args, &out, &stderr); code != 2 || out.Len() != 0 {
				t.Fatalf("invalid dataset reached evaluation: %d %s", code, stderr.String())
			}
		})
	}
}
