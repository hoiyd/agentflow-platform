package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/evaluation/failurecorpus"
)

// This workflow uses real owned suites and disposable Postgres. Approval is
// simulated only in temporary copies: tests must never approve repository data.
func TestHistoricalFailurePromotionPipeline(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("requires disposable Postgres for the mapped Artifact regression")
	}
	root := filepath.Join("..", "..", "..", "..")
	file, err := os.Open(filepath.Join(root, "examples", "failures", "corpus.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	seeds, err := failurecorpus.DecodeCorpus(file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	wanted := map[string]bool{"provider-continuation": true, "internal-task-state-write": true, "workspace-artifact-capability": true}
	corpus := `{"schema_version":"failure-corpus-v1","id":"historical-promotion-fixture","version":"1","cases":[]}`
	for _, item := range seeds.Cases {
		if !wanted[item.ID] {
			continue
		}
		delete(wanted, item.ID)
		item.Review = failurecorpus.Review{Status: "pending", Redaction: "pending"}
		pending, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		candidatePath := failureFile(t, "pending.json", string(pending))
		if _, code := failureCLI(t, "promote", "--candidate", candidatePath, "--corpus", failureFile(t, "corpus.json", corpus)); code != 2 {
			t.Fatal("pending historical candidate was promoted without human review")
		}
		item.Review = failurecorpus.Review{Status: "approved", Reviewer: "synthetic-fixture-reviewer", ReviewedAt: time.Now().UTC(), Redaction: "confirmed", Notes: "Simulated approval for workflow verification only; not a human approval of the repository candidate."}
		encoded, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		var code int
		corpus, code = failureCLI(t, "promote", "--candidate", failureFile(t, "reviewed-fixture.json", string(encoded)), "--corpus", failureFile(t, "corpus.json", corpus))
		if code != 0 {
			t.Fatalf("fixture promotion failed for %s", item.ID)
		}
	}
	if len(wanted) != 0 {
		t.Fatal("historical seed provenance was lost")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-json", "./internal/agent/toolloop", "./app", "-count=1", "-timeout=60s",
		"-run", "^(TestMultiRoundPreservesReasoningWithoutExposingIt|TestMultiRoundTaskStateWriteUsesRealStageOrTurnIdentity|TestObservableWorkspacePreservesArtifactReads)$")
	command.Dir = filepath.Join(root, "apps", "api")
	results, err := command.Output()
	if err != nil {
		t.Fatalf("owned regressions failed (retain go test output): %v\n%s", err, results)
	}
	out, code := failureCLI(t, "check", "--corpus", failureFile(t, "promoted.json", corpus),
		"--results", failureFile(t, "owned-tests.json", string(results)), "--results-revision", gitRevision(context.Background()), "--enforce")
	var report failurecorpus.Report
	if err := json.Unmarshal([]byte(out), &report); err != nil || code != 0 || !report.Gate.Passed || report.Gate.BlockingSamples != 3 {
		t.Fatalf("promotion workflow failed code=%d err=%v report=%s", code, err, out)
	}
	owners := map[string]bool{}
	for _, sample := range report.Samples {
		owners[sample.Owner] = true
	}
	if len(owners) != 3 {
		t.Fatal("expected three different owners")
	}
	if directory := os.Getenv("EVALUATION_REPORT_DIR"); directory != "" {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
		report.Limitations = append(report.Limitations, "The candidate/review transition was simulated in temporary fixtures; use the canonical corpus report for the actual approved seed gate.")
		encoded, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		for name, content := range map[string][]byte{"failure-promotion-fixture.json": encoded, "failure-promotion-tests.json": results, "failure-promotion-fixture-corpus.json": []byte(corpus)} {
			if err := os.WriteFile(filepath.Join(directory, name), content, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}
