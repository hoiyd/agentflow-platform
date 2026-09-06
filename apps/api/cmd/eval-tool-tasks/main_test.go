package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/tooleval"
)

func TestCLIRequiresExplicitAuthorizationAndBudgets(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "fixture-key")
	for _, args := range [][]string{
		nil, {"--unknown"}, {"--live"}, {"--live", "--model", "fixture"},
		{"--live", "--model", "fixture", "unexpected"},
	} {
		var out, stderr bytes.Buffer
		if code := run(context.Background(), args, &out, &stderr); code != 2 || out.Len() != 0 {
			t.Fatalf("unexpected CLI result: %d %s", code, out.String())
		}
	}
	t.Setenv("OPENAI_API_KEY", "")
	if code := run(context.Background(), []string{"--live", "--model", "fixture"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 2 {
		t.Fatal("missing key accepted")
	}
}

func TestCLIJSONSummaryAndGate(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "fixture-key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-key" {
			t.Error("missing credentials")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{}"}}],"usage":{"prompt_tokens":1000,"completion_tokens":10,"total_tokens":1010}}`))
	}))
	defer server.Close()
	args := []string{"--live", "--model", "fixture", "--base-url", server.URL, "--max-model-calls", "1", "--max-total-tokens", "10000"}
	for _, enforce := range []bool{false, true} {
		var out, stderr bytes.Buffer
		current := append([]string{}, args...)
		want := 0
		if enforce {
			current = append(current, "--enforce")
			want = 1
		}
		if code := run(context.Background(), current, &out, &stderr); code != want {
			t.Fatalf("code=%d stderr=%s", code, stderr.String())
		}
		var report tooleval.Report
		if err := json.Unmarshal(out.Bytes(), &report); err != nil || len(report.Samples) != 6 {
			t.Fatalf("invalid report: %v %s", err, out.String())
		}
		if !strings.Contains(stderr.String(), "verified=") || strings.Contains(out.String(), "fixture-key") {
			t.Fatal("summary missing or secret leaked")
		}
	}
	if code := run(context.Background(), args, failWriter{}, &bytes.Buffer{}); code != 2 {
		t.Fatal("output write failure ignored")
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }
