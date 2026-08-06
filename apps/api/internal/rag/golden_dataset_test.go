package rag

import (
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestResolveEvaluationCasesAcceptsVersionedGoldenDataset(t *testing.T) {
	t.Parallel()

	answerable := true
	cases, info, err := ResolveEvaluationCases(domain.RAGEvaluationRunRequest{Dataset: &domain.RAGGoldenDataset{
		SchemaVersion: domain.RAGGoldenDatasetSchemaVersion,
		ID:            "support-baseline",
		Version:       "1.0.0",
		Description:   "Support retrieval baseline",
		Tags:          []string{"smoke", "support"},
		Cases: []domain.RAGEvaluationCase{{
			ID: "auth-expired", Query: "How do I fix AUTH-7F31?", Answerable: &answerable,
			ExpectedSources:  []domain.RAGGoldenSource{{DocumentID: "doc-errors", ContentContains: []string{"AUTH-7F31"}}},
			ForbiddenSources: []domain.RAGGoldenSource{{DocumentID: "doc-deprecated"}},
			Tags:             []string{"identifier"},
		}},
	}})
	if err != nil {
		t.Fatalf("resolve dataset: %v", err)
	}
	if len(cases) != 1 || info == nil || info.ID != "support-baseline" || info.Version != "1.0.0" || info.SchemaVersion != domain.RAGGoldenDatasetSchemaVersion {
		t.Fatalf("unexpected resolved dataset: cases=%#v info=%#v", cases, info)
	}
}

func TestResolveEvaluationCasesRejectsInvalidGoldenDataset(t *testing.T) {
	t.Parallel()

	answerable := true
	unanswerable := false
	validCase := domain.RAGEvaluationCase{
		ID: "case-1", Query: "query", Answerable: &answerable,
		ExpectedSources: []domain.RAGGoldenSource{{DocumentID: "doc-1"}},
	}
	validDataset := func() *domain.RAGGoldenDataset {
		return &domain.RAGGoldenDataset{
			SchemaVersion: domain.RAGGoldenDatasetSchemaVersion,
			ID:            "dataset-1",
			Version:       "1.0.0",
			Cases:         []domain.RAGEvaluationCase{validCase},
		}
	}

	testCases := []struct {
		name  string
		build func() domain.RAGEvaluationRunRequest
		match string
	}{
		{name: "dataset and legacy cases", build: func() domain.RAGEvaluationRunRequest {
			return domain.RAGEvaluationRunRequest{Dataset: validDataset(), Cases: []domain.RAGEvaluationCase{validCase}}
		}, match: "either dataset or cases"},
		{name: "unsupported schema", build: func() domain.RAGEvaluationRunRequest {
			dataset := validDataset()
			dataset.SchemaVersion = "v2"
			return domain.RAGEvaluationRunRequest{Dataset: dataset}
		}, match: "schema_version"},
		{name: "duplicate case ids", build: func() domain.RAGEvaluationRunRequest {
			dataset := validDataset()
			dataset.Cases = append(dataset.Cases, validCase)
			return domain.RAGEvaluationRunRequest{Dataset: dataset}
		}, match: "duplicated"},
		{name: "missing answerable", build: func() domain.RAGEvaluationRunRequest {
			dataset := validDataset()
			dataset.Cases[0].Answerable = nil
			return domain.RAGEvaluationRunRequest{Dataset: dataset}
		}, match: "requires answerable"},
		{name: "answerable without expected source", build: func() domain.RAGEvaluationRunRequest {
			dataset := validDataset()
			dataset.Cases[0].ExpectedSources = nil
			return domain.RAGEvaluationRunRequest{Dataset: dataset}
		}, match: "requires expected_sources"},
		{name: "unanswerable with expected source", build: func() domain.RAGEvaluationRunRequest {
			dataset := validDataset()
			dataset.Cases[0].Answerable = &unanswerable
			return domain.RAGEvaluationRunRequest{Dataset: dataset}
		}, match: "cannot define expected_sources"},
		{name: "legacy expectation inside dataset", build: func() domain.RAGEvaluationRunRequest {
			dataset := validDataset()
			dataset.Cases[0].ExpectedSources = nil
			dataset.Cases[0].ExpectedDocumentIDs = []string{"doc-1"}
			return domain.RAGEvaluationRunRequest{Dataset: dataset}
		}, match: "legacy expected fields"},
		{name: "empty forbidden source", build: func() domain.RAGEvaluationRunRequest {
			dataset := validDataset()
			dataset.Cases[0].ForbiddenSources = []domain.RAGGoldenSource{{}}
			return domain.RAGEvaluationRunRequest{Dataset: dataset}
		}, match: "requires document_id"},
		{name: "duplicate tag", build: func() domain.RAGEvaluationRunRequest {
			dataset := validDataset()
			dataset.Tags = []string{"smoke", "smoke"}
			return domain.RAGEvaluationRunRequest{Dataset: dataset}
		}, match: "duplicate tag"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := ResolveEvaluationCases(testCase.build())
			if err == nil || !strings.Contains(err.Error(), testCase.match) {
				t.Fatalf("expected error containing %q, got %v", testCase.match, err)
			}
		})
	}
}

func TestEvaluateCaseAppliesAnswerableAndForbiddenSourceSemantics(t *testing.T) {
	t.Parallel()

	answerable := true
	unanswerable := false
	expected := domain.RetrievedDocumentChunk{
		Document: domain.Document{ID: "doc-current", SourceURI: "kb://current"},
		Chunk:    domain.DocumentChunk{ID: "chunk-current", Content: "AUTH-7F31 recovery steps"},
	}
	forbidden := domain.RetrievedDocumentChunk{
		Document: domain.Document{ID: "doc-deprecated", SourceURI: "kb://deprecated"},
		Chunk:    domain.DocumentChunk{ID: "chunk-old", Content: "obsolete recovery steps"},
	}

	matching := EvaluateCase(domain.RAGEvaluationCase{
		ID: "answerable", Query: "AUTH-7F31", Answerable: &answerable,
		ExpectedSources:  []domain.RAGGoldenSource{{DocumentID: "doc-current", SourceURI: "kb://current", ContentContains: []string{"auth-7f31"}}},
		ForbiddenSources: []domain.RAGGoldenSource{{DocumentID: "doc-deprecated"}},
	}, []domain.RetrievedDocumentChunk{expected})
	if !matching.Hit || matching.BestRank != 1 || !matching.HitAt1 {
		t.Fatalf("expected answerable case to match source, got %#v", matching)
	}

	leak := EvaluateCase(domain.RAGEvaluationCase{
		ID: "forbidden", Query: "AUTH-7F31", Answerable: &answerable,
		ExpectedSources:  []domain.RAGGoldenSource{{DocumentID: "doc-current"}},
		ForbiddenSources: []domain.RAGGoldenSource{{DocumentID: "doc-deprecated"}},
	}, []domain.RetrievedDocumentChunk{expected, forbidden})
	if leak.Hit || !strings.Contains(leak.FailureReason, "forbidden source matched at rank 2") {
		t.Fatalf("expected forbidden source to fail case, got %#v", leak)
	}

	noAnswer := EvaluateCase(domain.RAGEvaluationCase{
		ID: "no-answer", Query: "unknown", Answerable: &unanswerable,
	}, nil)
	if !noAnswer.Hit || noAnswer.Answerable || noAnswer.FailureReason != "" {
		t.Fatalf("expected empty retrieval to pass unanswerable case, got %#v", noAnswer)
	}

	forcedAnswer := EvaluateCase(domain.RAGEvaluationCase{
		ID: "forced-answer", Query: "unknown", Answerable: &unanswerable,
	}, []domain.RetrievedDocumentChunk{expected})
	if forcedAnswer.Hit || !strings.Contains(forcedAnswer.FailureReason, "expected no answer") {
		t.Fatalf("expected retrieved result to fail unanswerable case, got %#v", forcedAnswer)
	}
}
