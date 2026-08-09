package verification

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/domain"
)

const (
	defaultAnswerRelevanceMinimumScore      = 0.65
	defaultAnswerRelevanceMinimumCharacters = 20
)

type AnswerRelevanceConfig struct {
	MinimumScore            float64 `json:"minimum_score,omitempty"`
	MinimumAnswerCharacters int     `json:"minimum_answer_characters,omitempty"`
}

type AnswerRelevanceEmbedding struct {
	Vector     []float64
	Model      string
	Provider   string
	Estimated  bool
	Dimensions int
}

type AnswerRelevanceEmbedder func(context.Context, string) (AnswerRelevanceEmbedding, error)

// answerRelevanceVerifier owns the stable verifier contract while the frozen
// implementation version identifies this embedding-based scoring strategy.
// A later calibrated judge can replace the strategy without changing
// Completion Gate semantics or persisted Evidence structure.
type answerRelevanceVerifier struct {
	embed AnswerRelevanceEmbedder
}

func (answerRelevanceVerifier) Type() domain.VerifierType { return domain.VerifierAnswerRelevance }
func (answerRelevanceVerifier) Version() string           { return "answer-relevance-embedding-v1" }

func (answerRelevanceVerifier) NormalizeConfig(spec *domain.VerifierSpec) error {
	config, err := decodeConfig[AnswerRelevanceConfig](spec)
	if err != nil {
		return err
	}
	if config.MinimumScore == 0 {
		config.MinimumScore = defaultAnswerRelevanceMinimumScore
	}
	if config.MinimumScore < 0.05 || config.MinimumScore > 1 {
		return invalidContract("answer_relevance verifier " + spec.ID + " minimum_score must be between 0.05 and 1")
	}
	if config.MinimumAnswerCharacters == 0 {
		config.MinimumAnswerCharacters = defaultAnswerRelevanceMinimumCharacters
	}
	if config.MinimumAnswerCharacters < 1 || config.MinimumAnswerCharacters > 100_000 {
		return invalidContract("answer_relevance verifier " + spec.ID + " minimum_answer_characters must be between 1 and 100000")
	}
	return freezeConfig(spec, config)
}

func (v answerRelevanceVerifier) Verify(ctx context.Context, spec domain.VerifierSpec, subject Subject) Result {
	config, err := decodeConfig[AnswerRelevanceConfig](&spec)
	if err != nil {
		return blocked("answer relevance config is invalid")
	}
	if err := ctx.Err(); err != nil {
		return blocked("answer relevance verification was canceled")
	}
	question := strings.TrimSpace(subject.Question)
	if question == "" {
		return blocked("answer relevance requires the user question")
	}
	answer := strings.TrimSpace(subject.Value)
	scoringAnswer, questionRepetitionRemoved := withoutRepeatedQuestion(answer, question)
	answerCharacters := utf8.RuneCountInString(answer)
	scoringAnswerCharacters := utf8.RuneCountInString(strings.TrimSpace(scoringAnswer))
	if scoringAnswerCharacters < config.MinimumAnswerCharacters {
		details := answerRelevanceDetails(config, answerCharacters, scoringAnswerCharacters, questionRepetitionRemoved)
		details["score"] = 0.0
		return Result{
			Status:  domain.VerificationFailed,
			Summary: fmt.Sprintf("answer relevance failed: substantive answer has %d characters (minimum %d)", scoringAnswerCharacters, config.MinimumAnswerCharacters),
			Details: details, Artifacts: []Artifact{diagnosticArtifact(details)},
		}
	}
	if v.embed == nil {
		return blocked("answer relevance embedding model is unavailable")
	}

	questionEmbedding, err := v.embed(ctx, question)
	if err != nil {
		return blocked("answer relevance question embedding failed: " + err.Error())
	}
	answerEmbedding, err := v.embed(ctx, scoringAnswer)
	if err != nil {
		return blocked("answer relevance answer embedding failed: " + err.Error())
	}
	if questionEmbedding.Estimated || answerEmbedding.Estimated {
		return blocked("answer relevance requires non-estimated embeddings")
	}
	if questionEmbedding.Model != "" && answerEmbedding.Model != "" && questionEmbedding.Model != answerEmbedding.Model {
		return blocked("answer relevance embeddings were produced by different models")
	}
	if questionEmbedding.Provider != "" && answerEmbedding.Provider != "" && questionEmbedding.Provider != answerEmbedding.Provider {
		return blocked("answer relevance embeddings were produced by different providers")
	}
	if err := validateEmbeddingMetadata(questionEmbedding); err != nil {
		return blocked("answer relevance question embedding is invalid: " + err.Error())
	}
	if err := validateEmbeddingMetadata(answerEmbedding); err != nil {
		return blocked("answer relevance answer embedding is invalid: " + err.Error())
	}

	score, err := cosineSimilarity(questionEmbedding.Vector, answerEmbedding.Vector)
	if err != nil {
		return blocked("answer relevance embedding output is invalid: " + err.Error())
	}
	details := answerRelevanceDetails(config, answerCharacters, scoringAnswerCharacters, questionRepetitionRemoved)
	details["score"] = score
	details["embedding_model"] = firstNonEmpty(questionEmbedding.Model, answerEmbedding.Model)
	details["embedding_provider"] = firstNonEmpty(questionEmbedding.Provider, answerEmbedding.Provider)
	details["embedding_dimensions"] = len(questionEmbedding.Vector)

	status := domain.VerificationPassed
	summary := fmt.Sprintf("answer relevance passed with cosine similarity %.3f", score)
	if score < config.MinimumScore {
		status = domain.VerificationFailed
		summary = fmt.Sprintf("answer relevance failed with cosine similarity %.3f (minimum %.3f)", score, config.MinimumScore)
	}
	return Result{Status: status, Summary: summary, Details: details, Artifacts: []Artifact{diagnosticArtifact(details)}}
}

func answerRelevanceDetails(config AnswerRelevanceConfig, answerCharacters, scoringAnswerCharacters int, repetitionRemoved bool) map[string]any {
	return map[string]any{
		"algorithm":                     "cosine_similarity",
		"minimum_score":                 config.MinimumScore,
		"answer_characters":             answerCharacters,
		"substantive_answer_characters": scoringAnswerCharacters,
		"minimum_answer_characters":     config.MinimumAnswerCharacters,
		"question_repetition_removed":   repetitionRemoved,
	}
}

func validateEmbeddingMetadata(embedding AnswerRelevanceEmbedding) error {
	if embedding.Dimensions > 0 && embedding.Dimensions != len(embedding.Vector) {
		return fmt.Errorf("declared dimensions %d do not match vector length %d", embedding.Dimensions, len(embedding.Vector))
	}
	return nil
}

func cosineSimilarity(left, right []float64) (float64, error) {
	if len(left) == 0 || len(right) == 0 {
		return 0, errors.New("embedding vector is empty")
	}
	if len(left) != len(right) {
		return 0, fmt.Errorf("embedding dimensions differ: %d and %d", len(left), len(right))
	}
	dot := 0.0
	leftNorm := 0.0
	rightNorm := 0.0
	for index := range left {
		if math.IsNaN(left[index]) || math.IsInf(left[index], 0) || math.IsNaN(right[index]) || math.IsInf(right[index], 0) {
			return 0, errors.New("embedding vector contains a non-finite value")
		}
		dot += left[index] * right[index]
		leftNorm += left[index] * left[index]
		rightNorm += right[index] * right[index]
	}
	if leftNorm == 0 || rightNorm == 0 {
		return 0, errors.New("embedding vector has zero magnitude")
	}
	score := dot / (math.Sqrt(leftNorm) * math.Sqrt(rightNorm))
	return max(-1, min(1, score)), nil
}

func withoutRepeatedQuestion(answer, question string) (string, bool) {
	normalizedAnswer := strings.ToLower(answer)
	normalizedQuestion := strings.ToLower(strings.TrimSpace(question))
	if normalizedQuestion == "" || !strings.Contains(normalizedAnswer, normalizedQuestion) {
		return answer, false
	}
	return strings.ReplaceAll(normalizedAnswer, normalizedQuestion, " "), true
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
