package verification

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestAnswerRelevanceNormalizeConfigRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]any
	}{
		{name: "invalid shape", config: map[string]any{"minimum_score": "high"}},
		{name: "score too low", config: map[string]any{"minimum_score": 0.01}},
		{name: "score too high", config: map[string]any{"minimum_score": 1.01}},
		{name: "answer length negative", config: map[string]any{"minimum_answer_characters": -1}},
		{name: "answer length too high", config: map[string]any{"minimum_answer_characters": 100_001}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := domain.VerifierSpec{ID: "answer-relevance", Config: test.config}
			if err := (answerRelevanceVerifier{}).NormalizeConfig(&spec); !IsKind(err, ErrorInvalidContract) {
				t.Fatalf("expected invalid contract, got %v", err)
			}
		})
	}
}

func TestAnswerRelevanceVerifierBlocksInvalidInputAndEmbeddingErrors(t *testing.T) {
	validSpec := answerRelevanceSpec(5)
	invalidConfig := domain.VerifierSpec{ID: "answer-relevance", Config: map[string]any{"minimum_score": "high"}}
	if result := (answerRelevanceVerifier{}).Verify(context.Background(), invalidConfig, Subject{}); result.Status != domain.VerificationBlocked {
		t.Fatalf("invalid config must block verification: %#v", result)
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if result := (answerRelevanceVerifier{}).Verify(canceled, validSpec, SubjectForQuestionAnswer("question", "long enough answer")); result.Status != domain.VerificationBlocked {
		t.Fatalf("canceled verification must block: %#v", result)
	}

	short := answerRelevanceVerifier{embed: embeddingPair(validEmbedding(), validEmbedding())}
	if result := short.Verify(context.Background(), validSpec, SubjectForQuestionAnswer("question", "tiny")); result.Status != domain.VerificationFailed || result.Details["score"] != 0.0 {
		t.Fatalf("short substantive answer must fail before embedding: %#v", result)
	}

	want := errors.New("embedding unavailable")
	questionFailure := answerRelevanceVerifier{embed: func(context.Context, string) (AnswerRelevanceEmbedding, error) {
		return AnswerRelevanceEmbedding{}, want
	}}
	if result := questionFailure.Verify(context.Background(), validSpec, SubjectForQuestionAnswer("question", "long enough answer")); result.Status != domain.VerificationBlocked || !strings.Contains(result.Summary, want.Error()) {
		t.Fatalf("question embedding error must block: %#v", result)
	}

	calls := 0
	answerFailure := answerRelevanceVerifier{embed: func(context.Context, string) (AnswerRelevanceEmbedding, error) {
		calls++
		if calls == 2 {
			return AnswerRelevanceEmbedding{}, want
		}
		return validEmbedding(), nil
	}}
	if result := answerFailure.Verify(context.Background(), validSpec, SubjectForQuestionAnswer("question", "long enough answer")); result.Status != domain.VerificationBlocked || !strings.Contains(result.Summary, want.Error()) {
		t.Fatalf("answer embedding error must block: %#v", result)
	}
}

func TestAnswerRelevanceVerifierRejectsIncompatibleEmbeddings(t *testing.T) {
	base := validEmbedding()
	tests := []struct {
		name     string
		question AnswerRelevanceEmbedding
		answer   AnswerRelevanceEmbedding
	}{
		{name: "estimated question", question: withEstimated(base), answer: base},
		{name: "estimated answer", question: base, answer: withEstimated(base)},
		{name: "model mismatch", question: base, answer: withModel(base, "other-model")},
		{name: "provider mismatch", question: base, answer: withProvider(base, "other-provider")},
		{name: "question metadata dimensions", question: withDimensions(base, 3), answer: base},
		{name: "answer metadata dimensions", question: base, answer: withDimensions(base, 3)},
		{name: "empty question vector", question: withVector(base, nil), answer: base},
		{name: "empty answer vector", question: base, answer: withVector(base, nil)},
		{name: "different vector lengths", question: withVector(base, []float64{1}), answer: base},
		{name: "question NaN", question: withVector(base, []float64{math.NaN(), 1}), answer: base},
		{name: "question infinity", question: withVector(base, []float64{math.Inf(1), 1}), answer: base},
		{name: "answer NaN", question: base, answer: withVector(base, []float64{math.NaN(), 1})},
		{name: "answer infinity", question: base, answer: withVector(base, []float64{math.Inf(-1), 1})},
		{name: "zero question", question: withVector(base, []float64{0, 0}), answer: base},
		{name: "zero answer", question: base, answer: withVector(base, []float64{0, 0})},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verifier := answerRelevanceVerifier{embed: embeddingPair(test.question, test.answer)}
			result := verifier.Verify(context.Background(), answerRelevanceSpec(5), SubjectForQuestionAnswer("question", "long enough answer"))
			if result.Status != domain.VerificationBlocked {
				t.Fatalf("invalid embeddings must block: %#v", result)
			}
		})
	}
}

func TestAnswerRelevanceVerifierAcceptsMissingOptionalMetadata(t *testing.T) {
	embedding := AnswerRelevanceEmbedding{Vector: []float64{1, 0}, Dimensions: 2}
	verifier := answerRelevanceVerifier{embed: embeddingPair(embedding, embedding)}
	result := verifier.Verify(context.Background(), answerRelevanceSpec(5), SubjectForQuestionAnswer("question", "long enough answer"))
	if result.Status != domain.VerificationPassed || result.Details["embedding_model"] != "" || result.Details["embedding_provider"] != "" {
		t.Fatalf("optional metadata should not block valid vectors: %#v", result)
	}
}

func answerRelevanceSpec(minimumCharacters int) domain.VerifierSpec {
	return domain.VerifierSpec{ID: "answer-relevance", Config: map[string]any{
		"minimum_score": 0.65, "minimum_answer_characters": minimumCharacters,
	}}
}

func validEmbedding() AnswerRelevanceEmbedding {
	return AnswerRelevanceEmbedding{
		Vector: []float64{1, 0}, Model: "embedding-model", Provider: "provider", Dimensions: 2,
	}
}

func embeddingPair(question, answer AnswerRelevanceEmbedding) AnswerRelevanceEmbedder {
	calls := 0
	return func(context.Context, string) (AnswerRelevanceEmbedding, error) {
		calls++
		if calls == 1 {
			return question, nil
		}
		return answer, nil
	}
}

func withEstimated(embedding AnswerRelevanceEmbedding) AnswerRelevanceEmbedding {
	embedding.Estimated = true
	return embedding
}

func withModel(embedding AnswerRelevanceEmbedding, model string) AnswerRelevanceEmbedding {
	embedding.Model = model
	return embedding
}

func withProvider(embedding AnswerRelevanceEmbedding, provider string) AnswerRelevanceEmbedding {
	embedding.Provider = provider
	return embedding
}

func withDimensions(embedding AnswerRelevanceEmbedding, dimensions int) AnswerRelevanceEmbedding {
	embedding.Dimensions = dimensions
	return embedding
}

func withVector(embedding AnswerRelevanceEmbedding, vector []float64) AnswerRelevanceEmbedding {
	embedding.Vector = vector
	embedding.Dimensions = 0
	return embedding
}
