package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type referenceTaskManifest struct {
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

func TestReferenceTaskManifestMatchesSourceDatasets(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	content, err := os.ReadFile(filepath.Join(root, "examples", "reference-task.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest referenceTaskManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != "agentflow-reference-task-v1" {
		t.Fatalf("unexpected schema version %q", manifest.SchemaVersion)
	}

	taskIDs, splits, coverage := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, taskSet := range manifest.TaskSets {
		source := sourceCaseIDs(t, filepath.Join(root, filepath.FromSlash(taskSet.Dataset)))
		for _, task := range taskSet.Cases {
			if !source[task.ID] || taskIDs[task.ID] {
				t.Fatalf("missing or duplicate reference task %q", task.ID)
			}
			taskIDs[task.ID], splits[task.Split] = true, true
			for _, item := range task.Coverage {
				coverage[item] = true
			}
		}
	}
	if len(taskIDs) != 12 || !splits["calibration"] || !splits["holdout"] {
		t.Fatalf("reference suite must contain 12 split tasks: tasks=%d splits=%v", len(taskIDs), splits)
	}
	for _, required := range []string{"normal", "no_answer", "stale_version", "conflicting_sources", "long_context"} {
		if !coverage[required] {
			t.Fatalf("reference suite does not cover %q", required)
		}
	}

	evidenceFiles := map[string]bool{}
	for _, evidence := range manifest.OfflineEvidence {
		if evidence.File == "" || evidenceFiles[evidence.File] || !evidence.Simulated {
			t.Fatalf("invalid offline evidence entry %#v", evidence)
		}
		evidenceFiles[evidence.File] = true
	}
	for _, required := range []string{"rag-offline.json", "tool-task-protocol.json", "reference-recovery-replay.json"} {
		if !evidenceFiles[required] {
			t.Fatalf("reference evidence does not declare %q", required)
		}
	}
	failures := map[string]bool{}
	for _, scenario := range manifest.FailureScenarios {
		failures[scenario.ID] = true
	}
	for _, required := range []string{"dependency_failure", "budget_exhaustion", "recoverable_run", "uncertain_side_effect"} {
		if !failures[required] {
			t.Fatalf("reference suite does not declare %q", required)
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
