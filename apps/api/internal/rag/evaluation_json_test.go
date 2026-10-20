package rag

import (
	"agentflow-platform/apps/api/internal/domain"
	"strings"
	"testing"
)

// Unknown fields, duplicate names, null and case aliases must not erase a
// declared constraint at either the HTTP or offline dataset boundary.
func TestEvaluationJSONRejectsAmbiguousConstraints(t *testing.T) {
	for _, raw := range []string{
		`{"cases":[{"query":"x","metadata":[]}]}`,
		`{"cases":[{"query":"x","metadata":{"product":2}}]}`,
		`{"cases":[{"query":"x","require_filter":`,
		`{"cases":[`,
		strings.Repeat(" ", (2<<20)+1),
		strings.Repeat("[", 34) + "0" + strings.Repeat("]", 34),
		`{"cases":[{"query":"x","metadata_filter":null}]}`,
		`{"cases":[{"query":"x","metadata":null}]}`,
		`{"cases":[{"query":"x","metadata":{"product":null}}]}`,
		`{"cases":[{"query":"x","require_filter":null}]}`,
		`{"cases":[{"query":"x","metadata_filter":{},"metadata_filter":null}]}`,
		`{"cases":[{"query":"x","Metadata_Filter":{}}]}`,
		`{"cases":[{"query":"x","metadata_filter":{"schema_version":"knowledge-metadata-v1","where":{"all":null,"field":"document.metadata.product","op":"eq","value":"Atlas"}}}]}`,
		`{"cases":[{"query":"x","metadata_filter":{"schema_version":"knowledge-metadata-v1","where":{"field":"document.metadata.product","op":"eq","value":"Atlas","unknown":1}}}]}`,
		`{"cases":[{"query":"x","unknown":true}]}`,
		`{"cases":[{"query":"x"}]} {}`,
	} {
		var request domain.RAGEvaluationRunRequest
		if err := DecodeEvaluationJSON([]byte(raw), &request); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	var request domain.RAGEvaluationRunRequest
	if err := DecodeEvaluationJSON([]byte(`{"cases":[{"query":"x","metadata":{"CaseSensitive":"x","casesensitive":"y"}}]}`), &request); err != nil {
		t.Fatal(err)
	}
}
