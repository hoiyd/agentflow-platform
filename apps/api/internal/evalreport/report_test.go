package evalreport

import (
	"encoding/json"
	"testing"
)

func TestSharedReportEnvelopeIsStable(t *testing.T) {
	encoded, err := json.Marshal(struct {
		Identity
		Gate Gate `json:"gate"`
	}{Identity: Identity{ReportFormat: Format, DatasetID: "dataset"}, Gate: Gate{Passed: true, Reasons: []string{}}})
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"report_format":"agentflow-evaluation-report-v1","evaluation_kind":"","dataset_id":"dataset","dataset_version":"","dataset_hash":"","git_revision":"","started_at":"0001-01-01T00:00:00Z","completed_at":"0001-01-01T00:00:00Z","gate":{"passed":true,"blocking_samples":0,"blocking_failures":0,"reasons":[]}}` {
		t.Fatalf("unexpected report contract: %s", encoded)
	}
}
