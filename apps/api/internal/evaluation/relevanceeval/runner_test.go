package relevanceeval

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/modelprovider"
)

type fixtureEmbedder struct{ failOn string }

func (f fixtureEmbedder) EmbedText(_ context.Context, input string) (modelprovider.Embedding, error) {
	if f.failOn != "" && strings.Contains(input, f.failOn) {
		return modelprovider.Embedding{}, errors.New("embedding unavailable")
	}
	vector := []float64{1, 0}
	if strings.Contains(input, "off-topic") {
		vector = []float64{0, 1}
	}
	return modelprovider.Embedding{Vector: vector, Model: "fixture-embedding", Provider: "fixture", Dimensions: 2}, nil
}

func TestRunCalibratesOnlyOnCalibrationAndPassesHoldout(t *testing.T) {
	report, err := Run(context.Background(), fixtureEmbedder{}, Options{DatasetPath: writeDataset(t, false), Revision: "test",
		MinimumAnswerCharacters: 5, MaxFalseAcceptRate: 0.1, MaxFalseRejectRate: 0.1,
		Embedding: EmbeddingConfig{Profile: "fixture", ConfiguredModel: "fixture-embedding", Provider: "fixture", Dimensions: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Gate.Passed || report.Rollout.Mode != "eligible_for_required_gate" || report.Calibration.RecommendedThreshold < 0.05 {
		t.Fatalf("unexpected calibration result: gate=%+v rollout=%+v calibration=%+v", report.Gate, report.Rollout, report.Calibration)
	}
	holdout := report.Summary[SplitHoldout]
	if holdout.Correct != 2 || holdout.Confusion.TruePositive != 1 || holdout.Confusion.TrueNegative != 1 || report.Embedding.LogicalInputs != 8 {
		t.Fatalf("unexpected holdout evidence: summary=%+v embedding=%+v", holdout, report.Embedding)
	}
	if report.DatasetHash == "" || report.VerifierVersion != "answer-relevance-embedding-v1" || report.Samples[0].QuestionHash == "" || report.Samples[0].AnswerHash == "" {
		t.Fatalf("missing report provenance: %+v", report)
	}
}

func TestRunKeepsProviderFailureAndShortAnswerInHoldoutGate(t *testing.T) {
	report, err := Run(context.Background(), fixtureEmbedder{failOn: "provider-failure"}, Options{
		DatasetPath: writeDataset(t, true), MinimumAnswerCharacters: 20, MaxFalseAcceptRate: 0.1, MaxFalseRejectRate: 0.1})
	if err != nil {
		t.Fatal(err)
	}
	holdout := report.Summary[SplitHoldout]
	if report.Gate.Passed || report.Rollout.Mode != "warn_only" || holdout.NotEvaluated != 1 || holdout.Confusion.FalseNegative != 1 {
		t.Fatalf("failure paths disappeared from the gate: report=%+v holdout=%+v", report.Gate, holdout)
	}
	if report.Samples[2].DecisionReason != "answer_too_short" || report.Samples[3].FailureCode != "embedding_failed" {
		t.Fatalf("unexpected failure diagnostics: %+v", report.Samples)
	}
}

func TestDatasetValidationRejectsUnknownFieldsAndIncompleteSplits(t *testing.T) {
	for name, content := range map[string]string{
		"unknown":   `{"schema_version":"answer-relevance-dataset-v1","id":"x","version":"1","label_policy":"x","unknown":true,"cases":[]}`,
		"one-label": `{"schema_version":"answer-relevance-dataset-v1","id":"x","version":"1","label_policy":"x","cases":[{"id":"a","split":"calibration","coverage":["x"],"question":"q","answer":"a","expected_relevant":true},{"id":"b","split":"holdout","coverage":["x"],"question":"q","answer":"a","expected_relevant":true}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "dataset.json")
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Run(context.Background(), fixtureEmbedder{}, Options{DatasetPath: path}); err == nil {
				t.Fatal("invalid dataset accepted")
			}
		})
	}
}

func TestCanonicalDatasetAndEvidenceStayLinked(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "..")
	datasetPath := filepath.Join(root, "examples", "verification", "answer-relevance-dataset.v1.json")
	data, hash, err := loadDataset(datasetPath)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(root, "examples", "verification", "evidence", "qwen3-embedding-answer-relevance.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report Report
	if err := json.Unmarshal(content, &report); err != nil {
		t.Fatal(err)
	}
	if len(data.Cases) != 24 || report.DatasetID != data.ID || report.DatasetVersion != data.Version || report.DatasetHash != hash {
		t.Fatalf("calibration evidence drifted from its dataset: cases=%d report=%+v", len(data.Cases), report.Identity)
	}
	if report.Embedding.ActualModel != "qwen3-embedding:latest" || report.Calibration.RecommendedThreshold == 0 || report.Gate.Passed || report.Rollout.Mode != "warn_only" {
		t.Fatalf("unexpected frozen calibration conclusion: embedding=%+v calibration=%+v gate=%+v rollout=%+v", report.Embedding, report.Calibration, report.Gate, report.Rollout)
	}
}

func writeDataset(t *testing.T, failurePaths bool) string {
	t.Helper()
	holdoutRelevant := "This relevant answer directly addresses the request."
	holdoutIrrelevant := "This off-topic answer discusses a different subject."
	if failurePaths {
		holdoutRelevant = "yes"
		holdoutIrrelevant = "provider-failure off-topic answer"
	}
	content := `{
  "schema_version":"answer-relevance-dataset-v1",
  "id":"fixture",
  "version":"1",
  "label_policy":"Relevant means directly answers the main request; factual correctness is out of scope.",
  "cases":[
    {"id":"cal-positive","split":"calibration","coverage":["direct"],"question":"What is requested?","answer":"This relevant answer directly addresses the request.","expected_relevant":true},
    {"id":"cal-negative","split":"calibration","coverage":["off_topic"],"question":"What is requested?","answer":"This off-topic answer discusses a different subject.","expected_relevant":false},
    {"id":"hold-positive","split":"holdout","coverage":["direct"],"question":"What is requested?","answer":"` + holdoutRelevant + `","expected_relevant":true},
    {"id":"hold-negative","split":"holdout","coverage":["off_topic"],"question":"What is requested?","answer":"` + holdoutIrrelevant + `","expected_relevant":false}
  ]
}`
	path := filepath.Join(t.TempDir(), "dataset.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
