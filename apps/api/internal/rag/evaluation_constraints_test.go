package rag

import (
	"encoding/json"
	"strconv"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

// Missing or malformed constraints must fail before any query;
// intentionally unfiltered Golden cases must remain executable.
func TestEvaluationConstraintsPreflight(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"unfiltered", `{"cases":[{"id":"unfiltered","query":"rollback"}]}`, true},
		{"missing required", `{"cases":[{"query":"rollback","require_filter":true}]}`, false},
		{"empty required", `{"cases":[{"query":"rollback","require_filter":true,"metadata_filter":{}}]}`, false},
		{"unknown schema", `{"cases":[{"query":"rollback","metadata_filter":{"schema_version":"future","where":{"field":"document.metadata.product","op":"eq","value":"Atlas"}}}]}`, false},
		{"wrong type", `{"cases":[{"query":"rollback","metadata_filter":{"schema_version":"knowledge-metadata-v1","where":{"field":"document.metadata.priority","op":"gte","value":"50"}}}]}`, false},
		{"shared satisfies required", `{"metadata_filter":{"schema_version":"knowledge-metadata-v1","where":{"field":"document.metadata.product","op":"eq","value":"Atlas"}},"cases":[{"query":"rollback","require_filter":true}]}`, true},
		{"schema mismatch", `{"metadata_filter":{"schema_version":"knowledge-metadata-v1","where":{"field":"document.metadata.product","op":"eq","value":"Atlas"}},"cases":[{"query":"rollback","metadata_filter":{"schema_version":"future","where":{"field":"document.metadata.product","op":"eq","value":"Atlas"}}}]}`, false},
		{"credential filter", `{"cases":[{"query":"rollback","metadata_filter":{"schema_version":"knowledge-metadata-v1","where":{"field":"document.metadata.product","op":"eq","value":"Authorization: Bearer private-token"}}}]}`, false},
		{"later invalid case", `{"cases":[{"query":"first"},{"query":"second","metadata_filter":{}}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var request domain.RAGEvaluationRunRequest
			if err := json.Unmarshal([]byte(tc.body), &request); err != nil {
				t.Fatal(err)
			}
			dataset := domain.RAGGoldenDataset{SchemaVersion: domain.RAGGoldenDatasetSchemaVersion, ID: "constraints", Version: "1"}
			if err := json.Unmarshal([]byte(tc.body), &dataset); err != nil {
				t.Fatal(err)
			}
			for i := range dataset.Cases {
				dataset.Cases[i].ID = strconv.Itoa(i)
				dataset.Cases[i].Answerable = new(false)
			}
			request.Dataset = &dataset
			_, _, err := ResolveEvaluationCases(request)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
}
