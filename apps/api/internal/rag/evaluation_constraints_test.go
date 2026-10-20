package rag

import (
	"encoding/json"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

// Missing, malformed, or conflicting constraints must fail before any query;
// intentionally unfiltered legacy cases must remain executable.
func TestEvaluationConstraintsPreflight(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"legacy", `{"cases":[{"id":"old","query":"rollback"}]}`, true},
		{"missing required", `{"cases":[{"query":"rollback","require_filter":true}]}`, false},
		{"empty required", `{"cases":[{"query":"rollback","require_filter":true,"metadata":{}}]}`, false},
		{"unknown schema", `{"cases":[{"query":"rollback","metadata_filter":{"schema_version":"future","where":{"field":"document.metadata.product","op":"eq","value":"Atlas"}}}]}`, false},
		{"wrong type", `{"cases":[{"query":"rollback","metadata_filter":{"schema_version":"knowledge-metadata-v1","where":{"field":"document.metadata.priority","op":"gte","value":"50"}}}]}`, false},
		{"shared conflict", `{"metadata":{"product":"Atlas"},"cases":[{"query":"rollback","metadata":{"product":"Boreal"}}]}`, false},
		{"shared satisfies required", `{"metadata":{"product":"Atlas"},"cases":[{"query":"rollback","require_filter":true}]}`, true},
		{"schema mismatch", `{"metadata_filter":{"schema_version":"knowledge-metadata-v1","where":{"field":"document.metadata.product","op":"eq","value":"Atlas"}},"cases":[{"query":"rollback","metadata_filter":{"schema_version":"future","where":{"field":"document.metadata.product","op":"eq","value":"Atlas"}}}]}`, false},
		{"credential label", `{"metadata":{"note":"api_key=sk-abcdefgh"},"cases":[{"query":"rollback"}]}`, false},
		{"credential filter", `{"cases":[{"query":"rollback","metadata_filter":{"schema_version":"knowledge-metadata-v1","where":{"field":"document.metadata.product","op":"eq","value":"Authorization: Bearer private-token"}}}]}`, false},
		{"later invalid case", `{"cases":[{"query":"first"},{"query":"second","metadata_filter":{}}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var request domain.RAGEvaluationRunRequest
			if err := json.Unmarshal([]byte(tc.body), &request); err != nil {
				t.Fatal(err)
			}
			_, _, err := ResolveEvaluationCases(request)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
}
