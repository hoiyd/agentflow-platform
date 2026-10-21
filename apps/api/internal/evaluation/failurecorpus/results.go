package failurecorpus

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/evaluation/evalreport"
)

type Sample struct {
	CaseID         string     `json:"case_id"`
	Owner          string     `json:"owner"`
	SourceRevision string     `json:"source_revision"`
	Regression     Regression `json:"regression"`
	Status         string     `json:"status"`
}

type Report struct {
	evalreport.Identity
	SchemaVersion   string          `json:"schema_version"`
	ResultsRevision string          `json:"results_revision"`
	ResultsSHA256   string          `json:"results_sha256"`
	Samples         []Sample        `json:"samples"`
	Gate            evalreport.Gate `json:"gate"`
	Limitations     []string        `json:"limitations"`
}

type testState struct{ ran, passed, failed, skipped bool }

// Check consumes existing go test -json evidence, never a command from a
// manifest. A passing test without a completed package is incomplete evidence.
func Check(corpus Corpus, results io.Reader, resultsRevision, currentRevision string) (Report, error) {
	if !revision.MatchString(currentRevision) || resultsRevision != currentRevision || len(corpus.Cases) == 0 {
		return Report{}, errors.New("gate requires nonempty reviewed corpus and results from the current full Git revision")
	}
	started := time.Now().UTC()
	states, packages := map[string]*testState{}, map[string]string{}
	for _, item := range corpus.Cases {
		states[packageName(item.Regression.Package)+"/"+item.Regression.Test] = &testState{}
	}
	sum := sha256.New()
	scanner := bufio.NewScanner(io.TeeReader(results, sum))
	scanner.Buffer(make([]byte, 4096), 4<<20)
	for scanner.Scan() {
		var event struct{ Action, Package, Test string }
		if json.Unmarshal(scanner.Bytes(), &event) != nil {
			return Report{}, errors.New("test evidence must be go test -json output")
		}
		if event.Test == "" {
			if event.Action == "pass" || event.Action == "fail" || event.Action == "skip" {
				if packages[event.Package] != "fail" {
					packages[event.Package] = event.Action
				}
			}
			continue
		}
		root, _, _ := strings.Cut(event.Test, "/")
		if state := states[event.Package+"/"+root]; state != nil {
			switch event.Action {
			case "run":
				state.ran = true
			case "pass":
				state.passed = state.passed || event.Test == root
			case "fail":
				state.failed = true
			case "skip":
				state.skipped = true
			}
		}
	}
	if scanner.Err() != nil {
		return Report{}, errors.New("cannot read complete go test evidence (line limit 4 MiB)")
	}
	report := Report{
		Identity: evalreport.Identity{ReportFormat: evalreport.Format, EvaluationKind: "failure_regression", DatasetID: corpus.ID,
			DatasetVersion: corpus.Version, DatasetHash: hash(canonical(corpus)), GitRevision: currentRevision, StartedAt: started},
		SchemaVersion: "failure-regression-report-v1", ResultsRevision: resultsRevision, ResultsSHA256: hex.EncodeToString(sum.Sum(nil)),
		Gate:        evalreport.Gate{Passed: true, BlockingSamples: len(corpus.Cases), Reasons: []string{}},
		Limitations: []string{"human review is a Git-reviewed assertion, not a cryptographic attestation", "source references are not fetched; reviewers verify evidence and redaction", "test result revision is caller-declared; retain the command and original go test -json artifact", "existing deterministic regressions, not live model quality"},
	}
	for _, item := range canonical(corpus).Cases {
		pkg := packageName(item.Regression.Package)
		state, status := states[pkg+"/"+item.Regression.Test], "not_evaluated"
		switch {
		case state.failed || packages[pkg] == "fail":
			status = "failed"
		case state.skipped || packages[pkg] == "skip":
			status = "skipped"
		case state.ran && state.passed && packages[pkg] == "pass":
			status = "passed"
		}
		report.Samples = append(report.Samples, Sample{CaseID: item.ID, Owner: item.Owner, SourceRevision: item.SourceRevision, Regression: item.Regression, Status: status})
		if status != "passed" {
			report.Gate.Passed = false
			report.Gate.BlockingFailures++
			report.Gate.Reasons = append(report.Gate.Reasons, item.ID+": "+status)
		}
	}
	report.CompletedAt = time.Now().UTC()
	return report, nil
}

func packageName(relative string) string {
	return "agentflow-platform/apps/api/" + strings.TrimPrefix(relative, "./")
}
