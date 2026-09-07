package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/rageval"
	"agentflow-platform/apps/api/internal/tooleval"
)

func TestCLIRejectsUnknownSuiteAndUnauthorizedToolEvaluation(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "fixture-key")
	for _, args := range [][]string{nil, {"unknown"}, {"tool"}, {"tool", "--live"}, {"tool", "--live", "--model", "fixture"}, {"tool", "--live", "--model", "fixture", "unexpected"}} {
		var out, stderr bytes.Buffer
		if code := run(context.Background(), args, &out, &stderr); code != 2 || out.Len() != 0 {
			t.Fatalf("unexpected CLI result: %d %s", code, out.String())
		}
	}
	t.Setenv("OPENAI_API_KEY", "")
	if code := run(context.Background(), []string{"tool", "--live", "--model", "fixture", "--max-model-calls", "1", "--max-total-tokens", "1"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 2 {
		t.Fatal("missing API key accepted")
	}
}

func TestToolCLIJSONSummaryAndGate(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "fixture-key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{}"}}],"usage":{"prompt_tokens":1000,"completion_tokens":10,"total_tokens":1010}}`))
	}))
	defer server.Close()
	args := []string{"tool", "--live", "--model", "fixture", "--base-url", server.URL, "--max-model-calls", "1", "--max-total-tokens", "10000"}
	var out, stderr bytes.Buffer
	if code := run(context.Background(), append(args, "--enforce"), &out, &stderr); code != 1 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var report tooleval.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || len(report.Samples) != 6 || report.ReportFormat == "" {
		t.Fatalf("invalid report: %v %s", err, out.String())
	}
	if !strings.Contains(stderr.String(), "verified=") || strings.Contains(out.String(), "fixture-key") {
		t.Fatal("summary missing or secret leaked")
	}
	if code := run(context.Background(), args, failWriter{}, &bytes.Buffer{}); code != 2 {
		t.Fatal("output write failure ignored")
	}
}

func TestRAGCLIProducesReportAndEnforcesKnownBadDataset(t *testing.T) {
	dataset, manifest := writeRAGFixture(t, "missing phrase")
	args := []string{"rag", "--dataset", dataset, "--corpus-manifest", manifest, "--top-k", "1", "--min-similarity", "0", "--enforce"}
	var out, stderr bytes.Buffer
	if code := run(context.Background(), args, &out, &stderr); code != 1 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var report rageval.Report
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.Summary.Samples != 1 || report.Gate.Passed {
		t.Fatalf("invalid report: %v %#v", err, report)
	}
	if code := run(context.Background(), []string{"rag", "--dataset", "missing", "--corpus-manifest", manifest}, &bytes.Buffer{}, &bytes.Buffer{}); code != 2 {
		t.Fatal("missing dataset accepted")
	}
	if code := run(context.Background(), []string{"rag", "--ablation"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 2 {
		t.Fatal("ablation without baseline accepted")
	}
}

func TestRAGCLIComparesAValidBaseline(t *testing.T) {
	dataset, manifest := writeRAGFixture(t, "Frankfurt")
	args := []string{"rag", "--dataset", dataset, "--corpus-manifest", manifest, "--top-k", "1", "--min-similarity", "0"}
	var baseline bytes.Buffer
	if code := run(context.Background(), args, &baseline, &bytes.Buffer{}); code != 0 {
		t.Fatalf("baseline exit code %d", code)
	}
	path := filepath.Join(t.TempDir(), "baseline.json")
	if err := os.WriteFile(path, baseline.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	var candidate bytes.Buffer
	if code := run(context.Background(), append(args, "--baseline", path, "--enforce"), &candidate, &bytes.Buffer{}); code != 0 {
		t.Fatalf("candidate exit code %d", code)
	}
	var report rageval.Report
	if err := json.Unmarshal(candidate.Bytes(), &report); err != nil || report.Comparison == nil || !report.Comparison.Comparable {
		t.Fatalf("missing comparison: %v %#v", err, report.Comparison)
	}
	if err := os.WriteFile(path, []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}
	if code := run(context.Background(), append(args, "--baseline", path), &bytes.Buffer{}, &bytes.Buffer{}); code != 2 {
		t.Fatal("invalid baseline accepted")
	}
}

func writeRAGFixture(t *testing.T, expected string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	dataset := filepath.Join(dir, "dataset.json")
	manifest := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(filepath.Join(dir, "doc.md"), []byte("# Fact\n\nThe control plane is in Frankfurt."), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dataset, []byte(`{"schema_version":"rag-golden-dataset-v1","id":"fixture","version":"1","cases":[{"id":"fact","query":"control plane location","answerable":true,"expected_sources":[{"source_uri":"doc.md","content_contains":["`+expected+`"]}]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(`{"dataset_id":"fixture","version":"1","documents":[{"file":"doc.md","title":"Fact"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	return dataset, manifest
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }
