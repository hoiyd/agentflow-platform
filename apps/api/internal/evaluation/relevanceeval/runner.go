// Package relevanceeval calibrates the production answer_relevance verifier
// against human-labeled question/answer pairs. It never creates runtime Runs.
package relevanceeval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/evaluation/evalreport"
	"agentflow-platform/apps/api/internal/modelprovider"
	"agentflow-platform/apps/api/internal/verification"
)

const (
	SchemaVersion        = "answer-relevance-eval-v1"
	DatasetSchemaVersion = "answer-relevance-dataset-v1"
	SplitCalibration     = "calibration"
	SplitHoldout         = "holdout"
)

type Options struct {
	DatasetPath             string
	Revision                string
	MinimumAnswerCharacters int
	MaxFalseAcceptRate      float64
	MaxFalseRejectRate      float64
	Embedding               EmbeddingConfig
}

type EmbeddingConfig struct {
	Profile         string `json:"profile"`
	ConfiguredModel string `json:"configured_model"`
	Provider        string `json:"provider"`
	Dimensions      int    `json:"dimensions"`
	MaxCalls        int    `json:"max_calls"`
	MaxInputTokens  int    `json:"max_input_tokens"`
	RetryAttempts   int    `json:"retry_attempts"`
	TimeoutMS       int64  `json:"timeout_ms"`
}

type Dataset struct {
	SchemaVersion string           `json:"schema_version"`
	ID            string           `json:"id"`
	Version       string           `json:"version"`
	LabelPolicy   string           `json:"label_policy"`
	Cases         []EvaluationCase `json:"cases"`
}

type EvaluationCase struct {
	ID               string   `json:"id"`
	Split            string   `json:"split"`
	Coverage         []string `json:"coverage"`
	Question         string   `json:"question"`
	Answer           string   `json:"answer"`
	ExpectedRelevant bool     `json:"expected_relevant"`
}

type Sample struct {
	CaseID                      string   `json:"case_id"`
	Split                       string   `json:"split"`
	Coverage                    []string `json:"coverage"`
	Status                      string   `json:"status"`
	ExpectedRelevant            bool     `json:"expected_relevant"`
	PredictedRelevant           bool     `json:"predicted_relevant"`
	Correct                     bool     `json:"correct"`
	Score                       float64  `json:"score"`
	QuestionHash                string   `json:"question_hash"`
	AnswerHash                  string   `json:"answer_hash"`
	QuestionCharacters          int      `json:"question_characters"`
	AnswerCharacters            int      `json:"answer_characters"`
	SubstantiveAnswerCharacters int      `json:"substantive_answer_characters"`
	QuestionRepetitionRemoved   bool     `json:"question_repetition_removed"`
	DecisionReason              string   `json:"decision_reason"`
	FailureCode                 string   `json:"failure_code,omitempty"`
	FailureReason               string   `json:"failure_reason,omitempty"`
	LatencyMS                   int64    `json:"latency_ms"`
	EmbeddingModel              string   `json:"embedding_model,omitempty"`
	EmbeddingProvider           string   `json:"embedding_provider,omitempty"`
	EmbeddingDimensions         int      `json:"embedding_dimensions,omitempty"`
}

type ConfusionMatrix struct {
	TruePositive  int `json:"true_positive"`
	TrueNegative  int `json:"true_negative"`
	FalsePositive int `json:"false_positive"`
	FalseNegative int `json:"false_negative"`
}

type Summary struct {
	Samples         int             `json:"samples"`
	Evaluated       int             `json:"evaluated"`
	NotEvaluated    int             `json:"not_evaluated"`
	Correct         int             `json:"correct"`
	Confusion       ConfusionMatrix `json:"confusion_matrix"`
	Accuracy        float64         `json:"accuracy"`
	Precision       float64         `json:"precision"`
	Recall          float64         `json:"recall"`
	FalseAcceptRate float64         `json:"false_accept_rate"`
	FalseRejectRate float64         `json:"false_reject_rate"`
	MeanLatencyMS   float64         `json:"mean_latency_ms"`
}

type Calibration struct {
	Split                string  `json:"split"`
	Method               string  `json:"method"`
	Candidates           int     `json:"candidates"`
	RecommendedThreshold float64 `json:"recommended_threshold"`
	Calibration          Summary `json:"calibration_summary"`
	Holdout              Summary `json:"holdout_summary"`
}

type RolloutRecommendation struct {
	Mode   string `json:"mode"`
	Reason string `json:"reason"`
}

type Report struct {
	evalreport.Identity
	SchemaVersion   string                `json:"schema_version"`
	VerifierVersion string                `json:"verifier_version"`
	Config          Config                `json:"config"`
	Embedding       EmbeddingEvidence     `json:"embedding"`
	Samples         []Sample              `json:"samples"`
	Summary         map[string]Summary    `json:"summary"`
	Calibration     Calibration           `json:"calibration"`
	Rollout         RolloutRecommendation `json:"rollout_recommendation"`
	Gate            evalreport.Gate       `json:"gate"`
}

type Config struct {
	MinimumAnswerCharacters int     `json:"minimum_answer_characters"`
	MaxFalseAcceptRate      float64 `json:"max_false_accept_rate"`
	MaxFalseRejectRate      float64 `json:"max_false_reject_rate"`
}

type EmbeddingEvidence struct {
	EmbeddingConfig
	ActualModel      string `json:"actual_model,omitempty"`
	ActualProvider   string `json:"actual_provider,omitempty"`
	ActualDimensions int    `json:"actual_dimensions,omitempty"`
	LogicalInputs    int    `json:"logical_inputs"`
	PhysicalRequests int    `json:"physical_requests,omitempty"`
}

func Run(ctx context.Context, embedder modelprovider.Embedder, opts Options) (Report, error) {
	if embedder == nil {
		return Report{}, errors.New("answer relevance evaluation requires an embedding provider")
	}
	if opts.MinimumAnswerCharacters == 0 {
		opts.MinimumAnswerCharacters = 20
	}
	if opts.MinimumAnswerCharacters < 1 || opts.MinimumAnswerCharacters > 100_000 ||
		opts.MaxFalseAcceptRate < 0 || opts.MaxFalseAcceptRate > 1 || opts.MaxFalseRejectRate < 0 || opts.MaxFalseRejectRate > 1 {
		return Report{}, errors.New("minimum answer characters must be 1-100000 and error-rate limits must be within 0-1")
	}
	data, datasetHash, err := loadDataset(opts.DatasetPath)
	if err != nil {
		return Report{}, err
	}
	embeddingInputs := 0
	registry := verification.NewRegistry(verification.Options{AnswerRelevanceEmbedder: func(ctx context.Context, input string) (verification.AnswerRelevanceEmbedding, error) {
		embeddingInputs++
		embedding, err := embedder.EmbedText(ctx, input)
		return verification.AnswerRelevanceEmbedding{Vector: embedding.Vector, Model: embedding.Model, Provider: embedding.Provider,
			Estimated: embedding.Estimated, Dimensions: embedding.Dimensions}, err
	}})
	verifier, _ := registry.Resolve(domain.VerifierAnswerRelevance)
	spec := domain.VerifierSpec{ID: "answer-relevance-calibration", Type: domain.VerifierAnswerRelevance, Required: false,
		Config: map[string]any{"minimum_score": 0.05, "minimum_answer_characters": opts.MinimumAnswerCharacters}}
	if err := verifier.NormalizeConfig(&spec); err != nil {
		return Report{}, err
	}
	startedAt := time.Now().UTC()
	report := Report{Identity: evalreport.Identity{ReportFormat: evalreport.Format, EvaluationKind: "answer_relevance_calibration",
		DatasetID: data.ID, DatasetVersion: data.Version, DatasetHash: datasetHash,
		GitRevision: normalizeRevision(opts.Revision), StartedAt: startedAt}, SchemaVersion: SchemaVersion,
		VerifierVersion: verifier.Version(), Config: Config{MinimumAnswerCharacters: opts.MinimumAnswerCharacters,
			MaxFalseAcceptRate: opts.MaxFalseAcceptRate, MaxFalseRejectRate: opts.MaxFalseRejectRate},
		Embedding: EmbeddingEvidence{EmbeddingConfig: opts.Embedding}, Samples: make([]Sample, 0, len(data.Cases))}
	for _, item := range data.Cases {
		report.Samples = append(report.Samples, runSample(ctx, verifier, spec, item))
	}
	report.Calibration = calibrate(report.Samples, opts.MinimumAnswerCharacters)
	report.Samples = applyThreshold(report.Samples, report.Calibration.RecommendedThreshold, opts.MinimumAnswerCharacters)
	report.Summary = map[string]Summary{"overall": summarize(report.Samples), SplitCalibration: summarize(filterSamples(report.Samples, SplitCalibration)), SplitHoldout: summarize(filterSamples(report.Samples, SplitHoldout))}
	report.Calibration.Calibration = report.Summary[SplitCalibration]
	report.Calibration.Holdout = report.Summary[SplitHoldout]
	report.Embedding.LogicalInputs = embeddingInputs
	for _, sample := range report.Samples {
		if sample.EmbeddingModel != "" {
			report.Embedding.ActualModel, report.Embedding.ActualProvider, report.Embedding.ActualDimensions = sample.EmbeddingModel, sample.EmbeddingProvider, sample.EmbeddingDimensions
			break
		}
	}
	report.Gate = gate(report.Summary[SplitHoldout], opts)
	if report.Gate.Passed {
		report.Rollout = RolloutRecommendation{Mode: "eligible_for_required_gate", Reason: "holdout false-accept and false-reject rates are within the configured limits"}
	} else {
		report.Rollout = RolloutRecommendation{Mode: "warn_only", Reason: "keep required=false until the holdout gate passes for this embedding profile"}
	}
	report.CompletedAt = time.Now().UTC()
	return report, nil
}

func runSample(ctx context.Context, verifier verification.Verifier, spec domain.VerifierSpec, item EvaluationCase) Sample {
	started := time.Now()
	result := verifier.Verify(ctx, spec, verification.SubjectForQuestionAnswer(item.Question, item.Answer))
	sample := Sample{CaseID: item.ID, Split: item.Split, Coverage: append([]string(nil), item.Coverage...), Status: "completed",
		ExpectedRelevant: item.ExpectedRelevant, QuestionHash: digest([]byte(item.Question)), AnswerHash: digest([]byte(item.Answer)),
		QuestionCharacters: runeCount(item.Question), AnswerCharacters: runeCount(item.Answer), LatencyMS: time.Since(started).Milliseconds()}
	sample.Score = detailFloat(result.Details, "score")
	sample.SubstantiveAnswerCharacters = detailInt(result.Details, "substantive_answer_characters")
	sample.QuestionRepetitionRemoved = detailBool(result.Details, "question_repetition_removed")
	sample.EmbeddingModel = detailString(result.Details, "embedding_model")
	sample.EmbeddingProvider = detailString(result.Details, "embedding_provider")
	sample.EmbeddingDimensions = detailInt(result.Details, "embedding_dimensions")
	if result.Status == domain.VerificationBlocked {
		sample.Status, sample.FailureCode, sample.FailureReason = "not_evaluated", detailString(result.Details, "reason_code"), result.Summary
	}
	return sample
}

func calibrate(samples []Sample, minimumCharacters int) Calibration {
	calibration := filterSamples(samples, SplitCalibration)
	thresholds := []float64{0.05, 1}
	for _, sample := range calibration {
		if sample.Status != "completed" || sample.SubstantiveAnswerCharacters < minimumCharacters {
			continue
		}
		thresholds = append(thresholds, clampThreshold(sample.Score), clampThreshold(math.Nextafter(sample.Score, 1)))
	}
	thresholds = uniqueSorted(thresholds)
	best, bestCorrect, bestFalseAccept, bestFalseReject := 0.05, -1, 0, 0
	for _, threshold := range thresholds {
		summary := summarize(applyThreshold(calibration, threshold, minimumCharacters))
		if summary.Correct > bestCorrect || (summary.Correct == bestCorrect && summary.Confusion.FalsePositive < bestFalseAccept) ||
			(summary.Correct == bestCorrect && summary.Confusion.FalsePositive == bestFalseAccept && summary.Confusion.FalseNegative < bestFalseReject) ||
			(summary.Correct == bestCorrect && summary.Confusion.FalsePositive == bestFalseAccept && summary.Confusion.FalseNegative == bestFalseReject && threshold < best) {
			best, bestCorrect, bestFalseAccept, bestFalseReject = threshold, summary.Correct, summary.Confusion.FalsePositive, summary.Confusion.FalseNegative
		}
	}
	return Calibration{Split: SplitCalibration, Method: "maximize labeled accuracy, then minimize false accepts, false rejects, and threshold strictness",
		Candidates: len(thresholds), RecommendedThreshold: best}
}

func applyThreshold(samples []Sample, threshold float64, minimumCharacters int) []Sample {
	result := append([]Sample(nil), samples...)
	for index := range result {
		sample := &result[index]
		switch {
		case sample.Status != "completed":
			sample.DecisionReason = "not_evaluated"
		case sample.SubstantiveAnswerCharacters < minimumCharacters:
			sample.DecisionReason = "answer_too_short"
		case sample.Score < threshold:
			sample.DecisionReason = "score_below_threshold"
		default:
			sample.PredictedRelevant, sample.DecisionReason = true, "score_at_or_above_threshold"
		}
		sample.Correct = sample.Status == "completed" && sample.PredictedRelevant == sample.ExpectedRelevant
	}
	return result
}

func summarize(samples []Sample) Summary {
	result := Summary{Samples: len(samples)}
	latency := int64(0)
	for _, sample := range samples {
		if sample.Status != "completed" {
			result.NotEvaluated++
			continue
		}
		result.Evaluated++
		latency += sample.LatencyMS
		if sample.Correct {
			result.Correct++
		}
		switch {
		case sample.ExpectedRelevant && sample.PredictedRelevant:
			result.Confusion.TruePositive++
		case !sample.ExpectedRelevant && !sample.PredictedRelevant:
			result.Confusion.TrueNegative++
		case !sample.ExpectedRelevant && sample.PredictedRelevant:
			result.Confusion.FalsePositive++
		case sample.ExpectedRelevant && !sample.PredictedRelevant:
			result.Confusion.FalseNegative++
		}
	}
	if result.Evaluated > 0 {
		result.Accuracy = float64(result.Correct) / float64(result.Evaluated)
		result.MeanLatencyMS = float64(latency) / float64(result.Evaluated)
	}
	result.Precision = ratio(result.Confusion.TruePositive, result.Confusion.TruePositive+result.Confusion.FalsePositive)
	result.Recall = ratio(result.Confusion.TruePositive, result.Confusion.TruePositive+result.Confusion.FalseNegative)
	result.FalseAcceptRate = ratio(result.Confusion.FalsePositive, result.Confusion.TrueNegative+result.Confusion.FalsePositive)
	result.FalseRejectRate = ratio(result.Confusion.FalseNegative, result.Confusion.TruePositive+result.Confusion.FalseNegative)
	return result
}

func gate(holdout Summary, opts Options) evalreport.Gate {
	reasons := []string{}
	if holdout.Samples == 0 {
		reasons = append(reasons, "holdout split is empty")
	}
	if holdout.NotEvaluated > 0 {
		reasons = append(reasons, "one or more holdout samples were not evaluated")
	}
	if holdout.FalseAcceptRate > opts.MaxFalseAcceptRate {
		reasons = append(reasons, "holdout false-accept rate exceeds the configured limit")
	}
	if holdout.FalseRejectRate > opts.MaxFalseRejectRate {
		reasons = append(reasons, "holdout false-reject rate exceeds the configured limit")
	}
	return evalreport.Gate{Passed: len(reasons) == 0, BlockingSamples: holdout.Samples,
		BlockingFailures: holdout.NotEvaluated + holdout.Confusion.FalsePositive + holdout.Confusion.FalseNegative, Reasons: reasons}
}

func loadDataset(path string) (Dataset, string, error) {
	content, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return Dataset{}, "", fmt.Errorf("read answer relevance dataset: %w", err)
	}
	var data Dataset
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&data); err != nil {
		return Dataset{}, "", fmt.Errorf("parse answer relevance dataset: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Dataset{}, "", errors.New("answer relevance dataset must contain one JSON document")
	}
	if err := validateDataset(data); err != nil {
		return Dataset{}, "", err
	}
	return data, digest(content), nil
}

func validateDataset(data Dataset) error {
	if data.SchemaVersion != DatasetSchemaVersion || strings.TrimSpace(data.ID) == "" || strings.TrimSpace(data.Version) == "" || strings.TrimSpace(data.LabelPolicy) == "" || len(data.Cases) == 0 {
		return fmt.Errorf("answer relevance dataset requires schema %q, identity, label policy, and cases", DatasetSchemaVersion)
	}
	ids := map[string]bool{}
	type labels struct{ positive, negative bool }
	splits := map[string]*labels{SplitCalibration: {}, SplitHoldout: {}}
	for _, item := range data.Cases {
		if strings.TrimSpace(item.ID) == "" || ids[item.ID] {
			return fmt.Errorf("answer relevance dataset has missing or duplicate case id %q", item.ID)
		}
		ids[item.ID] = true
		bucket, ok := splits[item.Split]
		if !ok {
			return fmt.Errorf("case %q has invalid split %q", item.ID, item.Split)
		}
		if strings.TrimSpace(item.Question) == "" || strings.TrimSpace(item.Answer) == "" || len(item.Coverage) == 0 {
			return fmt.Errorf("case %q requires question, answer, and coverage", item.ID)
		}
		if item.ExpectedRelevant {
			bucket.positive = true
		} else {
			bucket.negative = true
		}
	}
	for split, labels := range splits {
		if !labels.positive || !labels.negative {
			return fmt.Errorf("%s split requires both relevant and irrelevant labels", split)
		}
	}
	return nil
}

func filterSamples(samples []Sample, split string) []Sample {
	result := make([]Sample, 0, len(samples))
	for _, sample := range samples {
		if sample.Split == split {
			result = append(result, sample)
		}
	}
	return result
}

func uniqueSorted(values []float64) []float64 {
	sort.Float64s(values)
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || math.Abs(result[len(result)-1]-value) > 1e-12 {
			result = append(result, value)
		}
	}
	return result
}

func clampThreshold(value float64) float64 { return max(0.05, min(1, value)) }
func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
func runeCount(value string) int { return len([]rune(strings.TrimSpace(value))) }
func normalizeRevision(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return strings.TrimSpace(value)
}
func digest(value []byte) string {
	hash := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(hash[:])
}
func detailFloat(values map[string]any, key string) float64 {
	value, _ := values[key].(float64)
	return value
}
func detailInt(values map[string]any, key string) int {
	switch value := values[key].(type) {
	case int:
		return value
	case float64:
		return int(value)
	default:
		return 0
	}
}
func detailBool(values map[string]any, key string) bool { value, _ := values[key].(bool); return value }
func detailString(values map[string]any, key string) string {
	if values[key] == nil {
		return ""
	}
	return fmt.Sprint(values[key])
}
