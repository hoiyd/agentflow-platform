package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Failure inventory: raw source content leaks; unsupported schema/owner; missing
// evidence/review/redaction; conflicting promotion; missing, skipped, failed or
// partial test results; stale revision; and output failures must fail closed.
func TestFailureCandidateProjectsMetadataWithoutCopyingPrivateContent(t *testing.T) {
	for _, artifact := range []string{
		`{"run":{"id":"run_0123abcd","status":"failed","error":"private-error"},"messages":[{"content":"private-prompt"}],"memory":"private-memory","credential":"private-key","tool_result":"private-tool-result"}`,
		`{"run":{"id":"run_0123abcd","status":"waiting_for_user"},"errors":["private-error"]}`,
		`{"report_format":"agentflow-evaluation-report-v1","schema_version":"context-quality-eval-v1","dataset_id":"fixture-dataset","dataset_hash":"abc","samples":[{"output":"private-output"}]}`,
	} {
		source := failureFile(t, "artifact.json", artifact)
		out, code := failureCLI(t, "candidate", "--artifact", source, "--id", "fixture-failure", "--owner", "provider",
			"--source-ref", "https://github.com/hoiyd/agentflow-platform/pull/151", "--source-revision", strings.Repeat("a", 40),
			"--evidence-ref", "https://example.com/retained/artifact.json")
		if code != 0 || strings.Contains(out, "private-") || strings.Contains(out, source) {
			t.Fatalf("unsafe candidate code=%d output=%s", code, out)
		}
		var candidate struct {
			Review   struct{ Status, Redaction string }
			Evidence []struct{ SHA256 string }
		}
		if err := json.Unmarshal([]byte(out), &candidate); err != nil || candidate.Review.Status != "pending" || candidate.Review.Redaction != "pending" || len(candidate.Evidence) != 1 || len(candidate.Evidence[0].SHA256) != 64 {
			t.Fatalf("candidate must require review: %s %v", out, err)
		}
	}
}

func TestFailurePromotionIsReviewedIdempotentAndContentAddressed(t *testing.T) {
	candidate := failureCandidate(t)
	path := failureFile(t, "candidate.json", candidate)
	empty := failureFile(t, "corpus.json", `{"schema_version":"failure-corpus-v1","id":"fixture-corpus","version":"1","cases":[]}`)
	promoted, code := failureCLI(t, "promote", "--candidate", path, "--corpus", empty)
	if code != 0 {
		t.Fatalf("promotion failed: %s", promoted)
	}
	corpus := failureFile(t, "promoted.json", promoted)
	repeated, code := failureCLI(t, "promote", "--candidate", path, "--corpus", corpus)
	if code != 0 || repeated != promoted {
		t.Fatal("repeat promotion changed the corpus")
	}
	conflict := failureFile(t, "conflict.json", strings.Replace(candidate, "Preserve provider continuation", "Changed expectation", 1))
	if _, code = failureCLI(t, "promote", "--candidate", conflict, "--corpus", corpus); code != 2 {
		t.Fatal("same ID silently replaced a reviewed case")
	}
	results := failureFile(t, "go-tests.json", failureEvents("pass", true))
	revision := gitRevision(context.Background())
	check := func(path string) map[string]any {
		t.Helper()
		out, code := failureCLI(t, "check", "--corpus", path, "--results", results, "--results-revision", revision, "--enforce")
		if code != 0 {
			t.Fatalf("gate code=%d: %s", code, out)
		}
		var report map[string]any
		if err := json.Unmarshal([]byte(out), &report); err != nil {
			t.Fatal(err)
		}
		return report
	}
	report := check(corpus)
	if report["report_format"] != "agentflow-evaluation-report-v1" || report["evaluation_kind"] != "failure_regression" || len(report["samples"].([]any)) != 1 {
		t.Fatalf("invalid report identity: %#v", report)
	}
	changed := failureFile(t, "changed-corpus.json", strings.Replace(promoted, "Preserve provider continuation", "New reviewed expectation", 1))
	if check(changed)["dataset_hash"] == report["dataset_hash"] {
		t.Fatal("dataset identity ignored changed content")
	}
	if check(corpus)["dataset_hash"] != report["dataset_hash"] {
		t.Fatal("dataset identity is unstable")
	}
	if _, code := failureCLI(t, "check", "--corpus", corpus, "--results", results, "--results-revision", strings.Repeat("b", 40), "--enforce"); code != 2 {
		t.Fatal("stale result revision accepted")
	}
}

func TestFailurePromotionRejectsUnsafeOrUnreviewedCandidates(t *testing.T) {
	valid := failureCandidate(t)
	corpus := failureFile(t, "corpus.json", `{"schema_version":"failure-corpus-v1","id":"fixture-corpus","version":"1","cases":[]}`)
	for _, scenario := range []struct{ name, old, replacement string }{
		{"schema", "failure-case-v1", "failure-case-v0"},
		{"owner", `"owner":"provider"`, `"owner":"unknown"`},
		{"pending", `"status":"approved"`, `"status":"pending"`},
		{"redaction", `"redaction":"confirmed"`, `"redaction":"pending"`},
		{"reviewer", `"reviewer":"fixture-reviewer"`, `"reviewer":""`},
		{"evidence", `"sha256":"` + strings.Repeat("c", 64) + `"`, `"sha256":""`},
		{"reference", "https://example.com/retained/artifact.json", "https://user:secret@example.com/private?token=secret"},
		{"revision", strings.Repeat("a", 40), "main"},
		{"target", "TestProtocol", "Test.*"},
		{"package", "./internal/agent/toolloop", "../../outside"},
		{"expected", "Preserve provider continuation", ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			path := failureFile(t, "candidate.json", strings.Replace(valid, scenario.old, scenario.replacement, 1))
			if _, code := failureCLI(t, "promote", "--candidate", path, "--corpus", corpus); code != 2 {
				t.Fatal("invalid candidate entered the corpus")
			}
		})
	}
	unknown := failureFile(t, "unknown.json", strings.TrimSuffix(valid, "}")+`,"prompt":"must-not-be-accepted"}`)
	if _, code := failureCLI(t, "promote", "--candidate", unknown, "--corpus", corpus); code != 2 {
		t.Fatal("unknown content-bearing field accepted")
	}
}

func TestFailureGateRetainsAllNonPassingCases(t *testing.T) {
	candidate := failureFile(t, "candidate.json", failureCandidate(t))
	empty := failureFile(t, "empty.json", `{"schema_version":"failure-corpus-v1","id":"fixture-corpus","version":"1","cases":[]}`)
	promoted, _ := failureCLI(t, "promote", "--candidate", candidate, "--corpus", empty)
	corpus := failureFile(t, "corpus.json", promoted)
	for _, scenario := range []struct{ name, events, status string }{
		{"failed", failureEvents("fail", true), "failed"},
		{"skipped", failureEvents("skip", true), "skipped"},
		{"missing", "", "not_evaluated"},
		{"partial", failureEvents("pass", false), "not_evaluated"},
		{"subtest", strings.Replace(failureEvents("pass", true), `"Action":"pass","Test":"TestProtocol"`, `"Action":"skip","Test":"TestProtocol/edge"`, 1), "skipped"},
		{"package failure", strings.Replace(failureEvents("pass", true), `"Action":"pass"}`, `"Action":"fail"}`, 1), "failed"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			results := failureFile(t, "results.json", scenario.events)
			out, code := failureCLI(t, "check", "--corpus", corpus, "--results", results, "--results-revision", gitRevision(context.Background()), "--enforce")
			var report struct {
				Samples []struct{ Status string }
				Gate    struct {
					Passed           bool
					BlockingSamples  int `json:"blocking_samples"`
					BlockingFailures int `json:"blocking_failures"`
				}
			}
			if err := json.Unmarshal([]byte(out), &report); err != nil || code != 1 || report.Gate.Passed || report.Gate.BlockingSamples != 1 || report.Gate.BlockingFailures != 1 || len(report.Samples) != 1 || report.Samples[0].Status != scenario.status || strings.Contains(out, "private-log") {
				t.Fatalf("denominator or privacy changed code=%d report=%s err=%v", code, out, err)
			}
		})
	}
	for _, invalid := range []string{`{"Action":`, `not json`} {
		if _, code := failureCLI(t, "check", "--corpus", corpus, "--results", failureFile(t, "bad.json", invalid), "--results-revision", gitRevision(context.Background())); code != 2 {
			t.Fatal("malformed results accepted")
		}
	}
}

func TestFailureCLIRejectsInvalidInputsAndOutputFailure(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"candidate"}, {"promote"}, {"check"}, {"check", "--unknown"}, {"candidate", "unexpected"}} {
		if _, code := failureCLI(t, args...); code != 2 {
			t.Fatalf("invalid command accepted: %v", args)
		}
	}
	base := []string{"failures", "candidate", "--id", "case-one", "--owner", "provider", "--source-ref", "https://example.com/fix", "--source-revision", strings.Repeat("a", 40), "--evidence-ref", "https://example.com/evidence"}
	for _, input := range []string{`{}`, `{"report_format":"obsolete","schema_version":"context-quality-eval-v1"}`, `{"run":{"id":"private-key","status":"failed"}}`, strings.Repeat("x", (8<<20)+1)} {
		args := append(append([]string{}, base...), "--artifact", failureFile(t, "bad-artifact.json", input))
		if code := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}); code != 2 {
			t.Fatal("invalid artifact accepted")
		}
	}
	valid := failureFile(t, "source.json", `{"run":{"id":"run_0123abcd","status":"failed"}}`)
	if code := run(context.Background(), append(base, "--artifact", valid), failWriter{}, &bytes.Buffer{}); code != 2 {
		t.Fatal("output failure ignored")
	}
}

func failureCandidate(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf(`{"schema_version":"failure-case-v1","id":"provider-continuation","owner":"provider","failure_class":"protocol_loss","source_ref":"https://github.com/hoiyd/agentflow-platform/pull/151","source_revision":"%s","evidence":[{"kind":"replay","ref":"https://example.com/retained/artifact.json","sha256":"%s"}],"input_boundary":"Same-Turn Tool continuation","expected_outcome":"Preserve provider continuation","regression":{"package":"./internal/agent/toolloop","test":"TestProtocol"},"review":{"status":"approved","reviewer":"fixture-reviewer","reviewed_at":"2026-10-21T00:00:00Z","redaction":"confirmed","notes":"Synthetic review for CLI boundary testing"}}`, strings.Repeat("a", 40), strings.Repeat("c", 64))
}

func failureEvents(action string, complete bool) string {
	const prefix = `"Package":"agentflow-platform/apps/api/internal/agent/toolloop",`
	result := "{" + prefix + `"Action":"run","Test":"TestProtocol"}` + "\n" +
		"{" + prefix + `"Action":"output","Output":"private-log"}` + "\n" +
		"{" + prefix + fmt.Sprintf(`"Action":%q,"Test":"TestProtocol"}`, action) + "\n"
	if complete {
		result += "{" + prefix + `"Action":"pass"}` + "\n"
	}
	return result
}

func failureCLI(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var out, stderr bytes.Buffer
	code := run(context.Background(), append([]string{"failures"}, args...), &out, &stderr)
	if code == 2 && out.Len() != 0 {
		t.Fatalf("rejected input wrote partial output: %s", out.String())
	}
	return out.String(), code
}

func failureFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
