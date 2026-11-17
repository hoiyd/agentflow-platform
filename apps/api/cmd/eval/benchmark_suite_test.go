package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"agentflow-platform/apps/api/internal/evaluation/rageval"
	"agentflow-platform/apps/api/internal/evaluation/tooleval"
)

type benchmarkSuiteManifest struct {
	SchemaVersion string `json:"schema_version"`
	TaskSets      []struct {
		Dataset string `json:"dataset"`
		Cases   []struct {
			ID       string   `json:"id"`
			Split    string   `json:"split"`
			Coverage []string `json:"coverage"`
		} `json:"cases"`
	} `json:"task_sets"`
	OfflineEvidence []struct {
		File      string `json:"file"`
		Simulated bool   `json:"simulated"`
	} `json:"offline_evidence"`
	FailureScenarios []struct {
		ID string `json:"id"`
	} `json:"failure_scenarios"`
}

func TestBenchmarkSuiteManifestMatchesSourceDatasets(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	content, err := os.ReadFile(filepath.Join(root, "examples", "benchmark-suite.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest benchmarkSuiteManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != "agentflow-benchmark-suite-v1" {
		t.Fatalf("unexpected schema version %q", manifest.SchemaVersion)
	}

	taskIDs, splits, coverage := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, taskSet := range manifest.TaskSets {
		source := sourceCaseIDs(t, filepath.Join(root, filepath.FromSlash(taskSet.Dataset)))
		for _, task := range taskSet.Cases {
			if !source[task.ID] || taskIDs[task.ID] {
				t.Fatalf("missing or duplicate benchmark task %q", task.ID)
			}
			taskIDs[task.ID], splits[task.Split] = true, true
			for _, item := range task.Coverage {
				coverage[item] = true
			}
		}
	}
	if len(taskIDs) != 12 || !splits["calibration"] || !splits["holdout"] {
		t.Fatalf("benchmark suite must contain 12 split tasks: tasks=%d splits=%v", len(taskIDs), splits)
	}
	for _, required := range []string{"normal", "no_answer", "stale_version", "conflicting_sources", "long_context"} {
		if !coverage[required] {
			t.Fatalf("benchmark suite does not cover %q", required)
		}
	}

	evidenceFiles := map[string]bool{}
	for _, evidence := range manifest.OfflineEvidence {
		if evidence.File == "" || evidenceFiles[evidence.File] || !evidence.Simulated {
			t.Fatalf("invalid offline evidence entry %#v", evidence)
		}
		evidenceFiles[evidence.File] = true
	}
	for _, required := range []string{"rag-offline.json", "tool-task-protocol.json", "benchmark-recovery-before-replay.json", "benchmark-recovery-replay.json"} {
		if !evidenceFiles[required] {
			t.Fatalf("benchmark evidence does not declare %q", required)
		}
	}
	failures := map[string]bool{}
	for _, scenario := range manifest.FailureScenarios {
		failures[scenario.ID] = true
	}
	for _, required := range []string{"dependency_failure", "budget_exhaustion", "recoverable_run", "uncertain_side_effect"} {
		if !failures[required] {
			t.Fatalf("benchmark suite does not declare %q", required)
		}
	}
}

func sourceCaseIDs(t *testing.T, path string) map[string]bool {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var dataset struct {
		Cases []struct {
			ID string `json:"id"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(content, &dataset); err != nil {
		t.Fatal(err)
	}
	result := make(map[string]bool, len(dataset.Cases))
	for _, item := range dataset.Cases {
		result[item.ID] = true
	}
	return result
}

// A manifest is not an execution plan: only the two CASE-001 groups are
// supported, and ambiguous declarations must fail before live requests.
func TestBenchmarkSuiteRejectsAmbiguousTasks(t *testing.T) {
	for _, taskSets := range []string{
		`[{"id":"unrelated","cases":[{"id":"one"}]}]`,
		`[{"id":"policy_retrieval","cases":[{"id":"one"},{"id":"one"}]},{"id":"long_record_evidence","cases":[{"id":"two"}]}]`,
		`[{"id":"policy_retrieval","cases":[{"id":""}]},{"id":"long_record_evidence","cases":[{"id":"two"}]}]`,
	} {
		path := filepath.Join(t.TempDir(), "suite.json")
		if err := os.WriteFile(path, []byte(`{"schema_version":"agentflow-benchmark-suite-v1","id":"fixture","version":"1","task_sets":`+taskSets+`}`), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := loadBenchmarkSuite(path); err == nil {
			t.Fatalf("ambiguous suite accepted: %s", taskSets)
		}
	}
}

func TestBenchmarkSamplesMatchDeclaredTasks(t *testing.T) {
	var suite benchmarkSuiteIdentity
	if err := json.Unmarshal([]byte(`{"task_sets":[{"id":"policy_retrieval","cases":[{"id":"policy"}]},{"id":"long_record_evidence","cases":[{"id":"record"}]}]}`), &suite); err != nil {
		t.Fatal(err)
	}
	ragSamples := []rageval.Sample{{CaseID: "policy"}}
	toolSamples := []tooleval.Sample{{TaskID: "record", Status: "not_evaluated"}, {TaskID: "record", Arm: "with_tools", Trial: 2}}
	if count, err := benchmarkTaskCoverage(suite, ragSamples, toolSamples); err != nil || count != 2 {
		t.Fatalf("declared failure samples lost: count=%d error=%v", count, err)
	}
	for _, invalid := range [][]tooleval.Sample{nil, append(append([]tooleval.Sample{}, toolSamples...), tooleval.Sample{TaskID: "extra"}), {{TaskID: "unrelated"}}} {
		if _, err := benchmarkTaskCoverage(suite, ragSamples, invalid); err == nil {
			t.Fatal("missing or unrelated tasks accepted")
		}
	}
	if _, err := benchmarkTaskCoverage(suite, []rageval.Sample{{CaseID: "unrelated"}}, toolSamples); err == nil {
		t.Fatal("unrelated retrieval report accepted")
	}
}
