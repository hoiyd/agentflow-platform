package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestParseOptionsRequiresAnOfflineInput(t *testing.T) {
	for _, args := range [][]string{{}, {"--seed-only"}, {"--corpus-manifest", "manifest.json"}, {"--dataset", "dataset.json", "--top-k", "0"}} {
		if _, err := parseOptions(args, ioDiscard{}); err == nil {
			t.Fatalf("expected invalid options for %#v", args)
		}
	}
	opts, err := parseOptions([]string{"--dataset", "dataset.json", "--top-k", "3", "--enforce"}, ioDiscard{})
	if err != nil || opts.datasetPath != "dataset.json" || opts.topK != 3 || !opts.enforce {
		t.Fatalf("unexpected options: %#v err=%v", opts, err)
	}
}

func TestDecodeJSONFileIsStrict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dataset.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":"rag-golden-dataset-v1","id":"baseline","version":"1","cases":[],"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeJSONFile[domain.RAGGoldenDataset](path); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestPathWithinRootRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	if _, err := pathWithinRoot(root, "../outside.md"); err == nil {
		t.Fatal("expected traversal to be rejected")
	}
	path, err := pathWithinRoot(root, "nested/document.md")
	if err != nil || path != filepath.Join(root, "nested", "document.md") {
		t.Fatalf("unexpected safe path: %q err=%v", path, err)
	}
}

func TestWriteResultAndGatingMisses(t *testing.T) {
	result := domain.RAGEvaluationRunResponse{
		Dataset: &domain.RAGGoldenDatasetInfo{ID: "baseline", Version: "1"},
		Summary: domain.RAGEvaluationSummary{Total: 2, HitAt1: 1, HitAt3: 1, HitAt5: 1, Misses: 1},
		Cases: []domain.RAGEvaluationCaseResult{
			{ID: "pass", Hit: true, Answerable: true, BestRank: 1},
			{ID: "diagnostic", FailureReason: "no match", Tags: []string{"non-blocking"}},
		},
	}
	var output bytes.Buffer
	if err := writeResult(&output, result, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "PASS [gating] pass: rank 1") || !strings.Contains(output.String(), "MISS [diagnostic] diagnostic: no match") {
		t.Fatalf("unexpected report: %s", output.String())
	}
	if misses := gatingMisses(result); len(misses) != 0 {
		t.Fatalf("diagnostic miss must not fail enforcement: %#v", misses)
	}
	result.Cases = append(result.Cases, domain.RAGEvaluationCaseResult{ID: "gate", FailureReason: "no match"})
	if misses := gatingMisses(result); len(misses) != 1 || misses[0].ID != "gate" {
		t.Fatalf("gating miss must fail enforcement: %#v", misses)
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(value []byte) (int, error) { return len(value), nil }
