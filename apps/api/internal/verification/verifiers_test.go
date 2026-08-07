package verification

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestCommandVerifierRunsWithoutShellAndEnforcesBoundaries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses standard Unix executables")
	}
	root := t.TempDir()
	registry := NewRegistry(Options{WorkspaceRoot: root, AllowedCommands: []string{"/usr/bin/printf", "/usr/bin/false"}, MaxArtifactBytes: 4})
	verifier, _ := registry.Resolve(domain.VerifierCommand)

	passed := verifier.Verify(context.Background(), domain.VerifierSpec{Config: map[string]any{"args": []string{"/usr/bin/printf", "abcdef"}}}, Subject{})
	if passed.Status != domain.VerificationPassed || len(passed.Artifacts) != 1 || passed.Artifacts[0].Content != "abcd" || !passed.Artifacts[0].Truncated || passed.Artifacts[0].ByteSize != 6 {
		t.Fatalf("unexpected command result: %#v", passed)
	}
	failed := verifier.Verify(context.Background(), domain.VerifierSpec{Config: map[string]any{"args": []string{"/usr/bin/false"}}}, Subject{})
	if failed.Status != domain.VerificationFailed || failed.ExitCode == nil || *failed.ExitCode == 0 {
		t.Fatalf("expected command assertion failure, got %#v", failed)
	}
	blocked := verifier.Verify(context.Background(), domain.VerifierSpec{Config: map[string]any{"args": []string{"rm", "-rf", "."}}}, Subject{})
	if blocked.Status != domain.VerificationBlocked {
		t.Fatalf("expected non-allowlisted command to be blocked, got %#v", blocked)
	}
}

func TestHTTPVerifierChecksStatusCapsOutputAndRestrictsHosts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("abcdef"))
	}))
	defer server.Close()
	registry := NewRegistry(Options{MaxArtifactBytes: 4})
	verifier, _ := registry.Resolve(domain.VerifierHTTP)

	passed := verifier.Verify(context.Background(), domain.VerifierSpec{Config: map[string]any{
		"method": http.MethodGet, "url": server.URL, "expected_status": http.StatusAccepted,
	}}, Subject{})
	if passed.Status != domain.VerificationPassed || len(passed.Artifacts) != 1 || passed.Artifacts[0].Content != "abcd" || !passed.Artifacts[0].Truncated {
		t.Fatalf("unexpected http result: %#v", passed)
	}
	failed := verifier.Verify(context.Background(), domain.VerifierSpec{Config: map[string]any{
		"method": http.MethodGet, "url": server.URL, "expected_status": http.StatusOK,
	}}, Subject{})
	if failed.Status != domain.VerificationFailed || !strings.Contains(failed.Summary, "got 202") {
		t.Fatalf("expected status mismatch, got %#v", failed)
	}
	blocked := verifier.Verify(context.Background(), domain.VerifierSpec{Config: map[string]any{
		"method": http.MethodGet, "url": "https://example.com", "expected_status": http.StatusOK,
	}}, Subject{})
	if blocked.Status != domain.VerificationBlocked {
		t.Fatalf("expected external host to be blocked, got %#v", blocked)
	}
}

func TestJSONSchemaVerifierValidatesRunOutput(t *testing.T) {
	registry := NewRegistry(Options{})
	verifier, _ := registry.Resolve(domain.VerifierJSONSchema)
	spec := domain.VerifierSpec{Config: map[string]any{"schema": map[string]any{
		"type":                 "object",
		"properties":           map[string]any{"status": map[string]any{"const": "ok"}},
		"required":             []any{"status"},
		"additionalProperties": false,
	}}}
	passed := verifier.Verify(context.Background(), spec, SubjectForRunOutput(`{"status":"ok"}`))
	if passed.Status != domain.VerificationPassed {
		t.Fatalf("expected schema match, got %#v", passed)
	}
	failed := verifier.Verify(context.Background(), spec, SubjectForRunOutput(`{"status":"no"}`))
	if failed.Status != domain.VerificationFailed || !strings.Contains(failed.Summary, "mismatch") {
		t.Fatalf("expected schema mismatch, got %#v", failed)
	}
}

func TestJSONSchemaVerifierBlocksRemoteReferences(t *testing.T) {
	registry := NewRegistry(Options{})
	verifier, _ := registry.Resolve(domain.VerifierJSONSchema)
	result := verifier.Verify(context.Background(), domain.VerifierSpec{
		Config: map[string]any{"schema": map[string]any{"$ref": "https://example.com/schema.json"}},
	}, SubjectForRunOutput(`{"status":"ok"}`))
	if result.Status != domain.VerificationBlocked || !strings.Contains(result.Summary, "remote schema reference is disabled") {
		t.Fatalf("remote schema reference was not blocked: %#v", result)
	}
}

func TestTextConstraintsVerifierChecksStructureAndContent(t *testing.T) {
	registry := NewRegistry(Options{})
	verifier, _ := registry.Resolve(domain.VerifierTextConstraints)
	spec := domain.VerifierSpec{ID: "report-text", Config: map[string]any{
		"min_words": 4, "max_words": 20,
		"required_phrases":  []string{"verified result"},
		"forbidden_phrases": []string{"TODO"},
		"required_headings": []string{"Findings"},
	}}
	if err := verifier.NormalizeConfig(&spec); err != nil {
		t.Fatalf("normalize text constraints: %v", err)
	}
	passed := verifier.Verify(context.Background(), spec, SubjectForRunOutput("# Findings\n\nThis is the verified result."))
	if passed.Status != domain.VerificationPassed || passed.Details["word_count"] == nil || len(passed.Artifacts) != 1 {
		t.Fatalf("expected text constraints to pass: %#v", passed)
	}
	failed := verifier.Verify(context.Background(), spec, SubjectForRunOutput("No result. TODO"))
	if failed.Status != domain.VerificationFailed || !strings.Contains(failed.Summary, "violation") {
		t.Fatalf("expected text constraints to fail: %#v", failed)
	}
}

func TestCitationVerifierChecksMarkdownLinksAndSourcePolicy(t *testing.T) {
	registry := NewRegistry(Options{})
	verifier, _ := registry.Resolve(domain.VerifierCitation)
	spec := domain.VerifierSpec{ID: "sources", Config: map[string]any{
		"min_citations": 2, "min_unique_hosts": 2, "require_https": true,
		"allowed_hosts": []string{"openai.com", "anthropic.com"},
	}}
	if err := verifier.NormalizeConfig(&spec); err != nil {
		t.Fatalf("normalize citation policy: %v", err)
	}
	passed := verifier.Verify(context.Background(), spec, SubjectForRunOutput(
		"See [OpenAI](https://openai.com/research) and <https://www.anthropic.com/research>."))
	if passed.Status != domain.VerificationPassed || passed.Details["citation_count"] != 2 {
		t.Fatalf("expected citation policy to pass: %#v", passed)
	}
	failed := verifier.Verify(context.Background(), spec, SubjectForRunOutput(
		"See [one](http://openai.com/research), [duplicate](http://openai.com/research), and https://anthropic.com/bare."))
	if failed.Status != domain.VerificationFailed || failed.Details["citation_count"] != 1 {
		t.Fatalf("expected duplicate, insecure, and bare URLs to fail policy: %#v", failed)
	}
}

func TestAnswerRelevanceVerifierScoresQuestionCoverage(t *testing.T) {
	registry := NewRegistry(Options{AnswerRelevanceEmbedder: answerRelevanceTestEmbedder})
	verifier, _ := registry.Resolve(domain.VerifierAnswerRelevance)
	spec := domain.VerifierSpec{ID: "answer-relevance", Config: map[string]any{
		"minimum_score": 0.65, "minimum_answer_characters": 10,
	}}
	if err := verifier.NormalizeConfig(&spec); err != nil {
		t.Fatalf("normalize answer relevance: %v", err)
	}

	passed := verifier.Verify(context.Background(), spec, SubjectForQuestionAnswer(
		"What are your opening hours?", "We are open from 9am to 10pm every day."))
	if passed.Status != domain.VerificationPassed || passed.Details["score"].(float64) < 0.65 || passed.Details["embedding_model"] != "test-embedding" {
		t.Fatalf("expected relevant answer to pass: %#v", passed)
	}

	failed := verifier.Verify(context.Background(), spec, SubjectForQuestionAnswer(
		"What are your opening hours?", "The stock market closed higher after the earnings report."))
	if failed.Status != domain.VerificationFailed || failed.Details["score"].(float64) >= 0.65 {
		t.Fatalf("expected unrelated answer to fail: %#v", failed)
	}

	parroted := verifier.Verify(context.Background(), spec, SubjectForQuestionAnswer(
		"What are your opening hours?", "What are your opening hours? I received your question."))
	if parroted.Status != domain.VerificationFailed || parroted.Details["question_repetition_removed"] != true {
		t.Fatalf("question repetition must not satisfy answer relevance: %#v", parroted)
	}
}

func TestAnswerRelevanceVerifierFreezesEmbeddingDefaults(t *testing.T) {
	registry := NewRegistry(Options{AnswerRelevanceEmbedder: answerRelevanceTestEmbedder})
	contract, err := registry.FreezeContract(&domain.CompletionContract{Verifiers: []domain.VerifierSpec{{
		ID: "answer-relevance", Type: domain.VerifierAnswerRelevance, Required: true,
	}}})
	if err != nil {
		t.Fatalf("freeze answer relevance contract: %v", err)
	}
	spec := contract.Verifiers[0]
	if spec.Version != "answer-relevance-embedding-v1" || spec.Config["minimum_score"] != 0.65 || spec.Config["minimum_answer_characters"] != float64(20) {
		t.Fatalf("unexpected embedding verifier defaults: %#v", spec)
	}
}

func TestAnswerRelevanceVerifierSupportsCJKAndRequiresQuestion(t *testing.T) {
	registry := NewRegistry(Options{AnswerRelevanceEmbedder: answerRelevanceTestEmbedder})
	verifier, _ := registry.Resolve(domain.VerifierAnswerRelevance)
	spec := domain.VerifierSpec{ID: "answer-relevance", Config: map[string]any{
		"minimum_score": 0.65, "minimum_answer_characters": 8,
	}}
	if err := verifier.NormalizeConfig(&spec); err != nil {
		t.Fatalf("normalize answer relevance: %v", err)
	}

	passed := verifier.Verify(context.Background(), spec, SubjectForQuestionAnswer(
		"披萨店的营业时间是什么？", "披萨店每天的营业时间是上午九点到晚上十点。"))
	if passed.Status != domain.VerificationPassed {
		t.Fatalf("expected relevant CJK answer to pass: %#v", passed)
	}
	blocked := verifier.Verify(context.Background(), spec, SubjectForRunOutput("We are open every day."))
	if blocked.Status != domain.VerificationBlocked {
		t.Fatalf("missing question must block relevance verification: %#v", blocked)
	}
}

func TestAnswerRelevanceVerifierRejectsEstimatedOrInvalidEmbeddings(t *testing.T) {
	spec := domain.VerifierSpec{ID: "answer-relevance", Config: map[string]any{
		"minimum_score": 0.65, "minimum_answer_characters": 5,
	}}
	estimated := answerRelevanceVerifier{embed: func(context.Context, string) (AnswerRelevanceEmbedding, error) {
		return AnswerRelevanceEmbedding{Vector: []float64{1, 0}, Model: "local-hash", Provider: "local", Estimated: true}, nil
	}}
	if result := estimated.Verify(context.Background(), spec, SubjectForQuestionAnswer("opening hours", "open every day")); result.Status != domain.VerificationBlocked {
		t.Fatalf("estimated embedding must not produce relevance evidence: %#v", result)
	}
	if result := (answerRelevanceVerifier{}).Verify(context.Background(), spec, SubjectForQuestionAnswer("opening hours", "open every day")); result.Status != domain.VerificationBlocked {
		t.Fatalf("missing embedding model must block relevance verification: %#v", result)
	}
	invalid := answerRelevanceVerifier{embed: func(_ context.Context, input string) (AnswerRelevanceEmbedding, error) {
		vector := []float64{1, 0}
		if strings.Contains(input, "answer") {
			vector = []float64{1}
		}
		return AnswerRelevanceEmbedding{Vector: vector, Model: "test", Provider: "test"}, nil
	}}
	if result := invalid.Verify(context.Background(), spec, SubjectForQuestionAnswer("question", "long enough answer")); result.Status != domain.VerificationBlocked {
		t.Fatalf("dimension mismatch must block relevance verification: %#v", result)
	}
}

func answerRelevanceTestEmbedder(_ context.Context, input string) (AnswerRelevanceEmbedding, error) {
	lower := strings.ToLower(input)
	vector := []float64{0, 1, 0}
	switch {
	case strings.Contains(input, "营业时间") || strings.Contains(input, "上午九点"):
		vector = []float64{0, 0, 1}
	case strings.Contains(lower, "opening hours"):
		vector = []float64{1, 0, 0}
	case strings.Contains(lower, "open from"):
		vector = []float64{0.98, 0.1, 0}
	}
	return AnswerRelevanceEmbedding{Vector: vector, Model: "test-embedding", Provider: "test", Dimensions: len(vector)}, nil
}

func TestVerifierNormalizationRejectsUnknownConfigFields(t *testing.T) {
	registry := NewRegistry(Options{})
	_, err := registry.FreezeContract(&domain.CompletionContract{Verifiers: []domain.VerifierSpec{{
		ID: "text", Type: domain.VerifierTextConstraints, Required: true,
		Config: map[string]any{"min_words": 1, "unexpected": true},
	}}})
	if !IsKind(err, ErrorInvalidContract) {
		t.Fatalf("expected strict config error, got %v", err)
	}
}
