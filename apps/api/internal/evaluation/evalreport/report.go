package evalreport

import "time"

const Format = "agentflow-evaluation-report-v1"

// Identity is the shared provenance envelope for every offline evaluation.
// Domain-specific report schemas remain independent, while their inputs and
// execution identity stay comparable and machine-readable.
type Identity struct {
	ReportFormat   string    `json:"report_format"`
	EvaluationKind string    `json:"evaluation_kind"`
	DatasetID      string    `json:"dataset_id"`
	DatasetVersion string    `json:"dataset_version"`
	DatasetHash    string    `json:"dataset_hash"`
	GitRevision    string    `json:"git_revision"`
	StartedAt      time.Time `json:"started_at"`
	CompletedAt    time.Time `json:"completed_at"`
}

type Gate struct {
	Passed           bool     `json:"passed"`
	BlockingSamples  int      `json:"blocking_samples"`
	BlockingFailures int      `json:"blocking_failures"`
	Reasons          []string `json:"reasons"`
}
