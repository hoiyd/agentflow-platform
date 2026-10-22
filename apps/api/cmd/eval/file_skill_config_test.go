package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Setup failure inventory: consent/target/budgets, invalid or ambiguous tasks,
// missing dependencies and malformed packages must reject before any spending.
func TestFileSkillInvalidSetupNeverSpends(t *testing.T) {
	t.Setenv("FILE_SKILL_FIXTURE_KEY", "fixture-only")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid setup reached model")
		http.Error(w, "unexpected", 500)
	}))
	defer server.Close()
	base := []string{"tool", "--profile", "file_skill", "--live", "--model", "fixture-model", "--base-url", server.URL, "--credential-env", "FILE_SKILL_FIXTURE_KEY", "--max-model-calls", "20", "--max-total-tokens", "100000", "--dataset", "../../../../examples/tools/file-skill-v1/dataset.json", "--skill-dir", "../../../../examples/tools/file-skill-v1/skills"}
	for _, extra := range [][]string{{"--live=false"}, {"--max-model-calls", "0"}, {"--max-total-tokens", "1"}, {"--timeout", "6m"}, {"--suite-timeout", "1ms"}, {"--evidence-mode", "unknown"}, {"--base-url", "https://user:secret@example.invalid"}, {"--base-url", "https://example.invalid?token=secret"}, {"--model", "\n"}, {"--max-token-ratio", "NaN"}, {"--max-extra-model-requests", "-1"}, {"--trials", "21"}, {"--credential-env", "MISSING_EVAL_CREDENTIAL"}, {"--profile", "unknown"}} {
		t.Run(strings.Join(extra, " "), func(t *testing.T) {
			var out, stderr bytes.Buffer
			if run(t.Context(), append(append([]string{}, base...), extra...), &out, &stderr) != 2 || out.Len() != 0 || strings.Contains(stderr.String(), "Postgres") {
				t.Fatalf("setup not rejected first: %s", stderr.String())
			}
		})
	}
	for _, scenario := range []string{"unknown_field", "duplicate_task", "no_file", "no_neighbor", "explicit_input", "missing_dataset", "missing_dependency", "missing_package"} {
		t.Run(scenario, func(t *testing.T) {
			content, err := os.ReadFile("../../../../examples/tools/file-skill-v1/dataset.json")
			if err != nil {
				t.Fatal(err)
			}
			var data map[string]any
			if err = json.Unmarshal(content, &data); err != nil {
				t.Fatal(err)
			}
			tasks := data["tasks"].([]any)
			first := tasks[0].(map[string]any)
			second := tasks[1].(map[string]any)
			root := t.TempDir()
			dataset := filepath.Join(root, "dataset.json")
			skills := "../../../../examples/tools/file-skill-v1/skills"
			switch scenario {
			case "unknown_field":
				data["unknown"] = true
			case "duplicate_task":
				second["id"] = first["id"]
			case "no_file":
				first["expected_file"] = ""
			case "no_neighbor":
				second["expect_skill"] = true
			case "explicit_input":
				first["input"] = "/skill:release-report hello"
			case "missing_package":
				skills = root
			case "missing_dependency":
				skills = root
				pkg := filepath.Join(root, "release-report")
				if err = os.Mkdir(pkg, 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(pkg, "SKILL.md"), []byte("---\nname: release-report\ndescription: synthetic task\nmetadata:\n  agentflow-required-tools: unavailable_tool\n---\nUse the missing dependency.\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			encoded, _ := json.Marshal(data)
			if err = os.WriteFile(dataset, encoded, 0600); err != nil {
				t.Fatal(err)
			}
			if scenario == "missing_dataset" {
				dataset = filepath.Join(root, "missing.json")
			}
			var out, stderr bytes.Buffer
			if code := run(t.Context(), append(append([]string{}, base...), "--dataset", dataset, "--skill-dir", skills), &out, &stderr); code != 2 || out.Len() != 0 || strings.Contains(stderr.String(), "Postgres") {
				t.Fatalf("dataset/package not rejected before execution: %s", stderr.String())
			}
		})
	}
}

func TestToolProfileDoesNotSilentlyIgnoreExperimentFlags(t *testing.T) {
	var out, stderr bytes.Buffer
	if run(t.Context(), []string{"tool", "--dataset", "custom.json"}, &out, &stderr) != 2 || !strings.Contains(stderr.String(), "require --profile file_skill") {
		t.Fatal("file experiment flags were silently ignored")
	}
}
