package verification

import (
	"context"
	"errors"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestGroundedAnswerAcceptanceCases(t *testing.T) {
	t.Parallel()

	verifier := groundedAnswerVerifier{}
	spec := domain.VerifierSpec{ID: "grounding", Config: map[string]any{
		"minimum_claim_support": 0.5,
		"no_answer_phrases":     []string{"insufficient evidence"},
	}}
	sources := []GroundingSource{{SourceID: "S1", Content: "The production control plane runs in AWS eu-central-1 in Frankfurt. Audit events are retained for 35 days."}}
	for _, testCase := range []struct {
		name    string
		answer  string
		sources []GroundingSource
		status  domain.VerificationStatus
	}{
		{name: "supported claim", answer: "The production control plane runs in AWS `eu-central-1` [S1].", sources: sources, status: domain.VerificationPassed},
		{name: "partial answer", answer: "Audit events are retained for 35 days [S1]. There is insufficient evidence for the archive retention period.", sources: sources, status: domain.VerificationPassed},
		{name: "no answer", answer: "There is insufficient evidence to answer this question.", status: domain.VerificationPassed},
		{name: "no-answer phrase with assertion", answer: "There is insufficient evidence, but the service runs in Tokyo.", status: domain.VerificationFailed},
		{name: "refusal despite evidence", answer: "There is insufficient evidence to answer this question.", sources: sources, status: domain.VerificationFailed},
		{name: "claim without evidence", answer: "The service runs in Tokyo.", status: domain.VerificationFailed},
		{name: "missing citation", answer: "Audit events are retained for 35 days.", sources: sources, status: domain.VerificationFailed},
		{name: "out of range citation", answer: "Audit events are retained for 35 days [S9].", sources: sources, status: domain.VerificationFailed},
		{name: "irrelevant existing citation", answer: "The lunar capacitor is repaired weekly [S1].", sources: sources, status: domain.VerificationFailed},
		{name: "false assertion", answer: "The production control plane runs in AWS `us-east-1` [S1].", sources: sources, status: domain.VerificationFailed},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			result := verifier.Verify(context.Background(), spec, SubjectForGroundedQuestionAnswer("question", testCase.answer, testCase.sources, nil))
			if result.Status != testCase.status {
				t.Fatalf("got %s, want %s: %#v", result.Status, testCase.status, result)
			}
		})
	}
}

func TestGroundedAnswerBlocksUnavailableEvidenceAndInvalidConfig(t *testing.T) {
	t.Parallel()

	verifier := groundedAnswerVerifier{}
	spec := domain.VerifierSpec{ID: "grounding", Config: map[string]any{"minimum_claim_support": 0.5}}
	subject := SubjectForGroundedQuestionAnswer("question", "answer [S1]", nil, errors.New("document was deleted"))
	if result := verifier.Verify(context.Background(), spec, subject); result.Status != domain.VerificationBlocked {
		t.Fatalf("unavailable evidence did not block: %#v", result)
	}
	invalid := domain.VerifierSpec{ID: "grounding", Config: map[string]any{"minimum_claim_support": 2}}
	if err := verifier.NormalizeConfig(&invalid); err == nil {
		t.Fatal("invalid support threshold accepted")
	}

	defaults := domain.VerifierSpec{ID: "grounding"}
	if err := verifier.NormalizeConfig(&defaults); err != nil || defaults.Config["minimum_claim_support"] != 0.5 {
		t.Fatalf("default config was not frozen: %#v err=%v", defaults.Config, err)
	}
	emptyPhrase := domain.VerifierSpec{ID: "grounding", Config: map[string]any{"no_answer_phrases": []string{" "}}}
	if err := verifier.NormalizeConfig(&emptyPhrase); err == nil {
		t.Fatal("empty no-answer phrase accepted")
	}
	tooMany := make([]string, 21)
	for index := range tooMany {
		tooMany[index] = "phrase"
	}
	tooManyPhrases := domain.VerifierSpec{ID: "grounding", Config: map[string]any{"no_answer_phrases": tooMany}}
	if err := verifier.NormalizeConfig(&tooManyPhrases); err == nil {
		t.Fatal("unbounded no-answer phrases accepted")
	}
	malformed := domain.VerifierSpec{ID: "grounding", Config: map[string]any{"minimum_claim_support": "high"}}
	if result := verifier.Verify(context.Background(), malformed, Subject{}); result.Status != domain.VerificationBlocked {
		t.Fatalf("malformed frozen config did not block: %#v", result)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if result := verifier.Verify(canceled, spec, Subject{}); result.Status != domain.VerificationBlocked {
		t.Fatalf("canceled verification did not block: %#v", result)
	}
	longClaim := strings.Repeat("unsupported ", 30) + "[S1]."
	result := verifier.Verify(context.Background(), spec, SubjectForGroundedQuestionAnswer("question", longClaim, []GroundingSource{{SourceID: "S1", Content: "other"}}, nil))
	violations := result.Details["violations"].([]map[string]any)
	if len(violations[0]["claim"].(string)) > 243 {
		t.Fatal("claim diagnostic was not bounded")
	}
}

func TestGroundedSubjectHashIncludesEvidence(t *testing.T) {
	t.Parallel()

	first := SubjectForGroundedQuestionAnswer("question", "answer [S1]", []GroundingSource{{SourceID: "S1", Content: "first"}}, nil)
	second := SubjectForGroundedQuestionAnswer("question", "answer [S1]", []GroundingSource{{SourceID: "S1", Content: "second"}}, nil)
	if first.Hash == second.Hash {
		t.Fatal("grounding evidence was omitted from the subject hash")
	}
}
