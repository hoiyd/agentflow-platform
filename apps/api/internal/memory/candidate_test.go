package memory

import (
	"context"
	"errors"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestRuleBasedCandidateExtractorUsesExplicitDurabilitySignals(t *testing.T) {
	tests := []struct {
		content string
		kind    string
		reason  string
		want    string
	}{
		{"Remember that AgentFlow uses typed events.", "fact", CandidateReasonExplicit, "AgentFlow uses typed events."},
		{"I prefer concise answers.", "preference", CandidateReasonPreference, "concise answers."},
		{"项目约定：后端使用 Go。", "project_convention", CandidateReasonConvention, "后端使用 Go。"},
	}
	extractor := RuleBasedCandidateExtractor{}
	for _, test := range tests {
		draft, ok, err := extractor.Extract(context.Background(), domain.Message{Role: "user", Content: test.content})
		if err != nil || !ok || draft.Kind != test.kind || draft.ExtractionReason != test.reason || draft.Content != test.want || draft.Confidence != 1 {
			t.Fatalf("extract %q: %#v ok=%v", test.content, draft, ok)
		}
	}
	if _, ok, err := extractor.Extract(context.Background(), domain.Message{Role: "user", Content: "How does AgentFlow work?"}); err != nil || ok {
		t.Fatal("ordinary chat should not produce a memory candidate")
	}
	if _, ok, err := extractor.Extract(context.Background(), domain.Message{Role: "assistant", Content: "Remember that this is true."}); err != nil || ok {
		t.Fatal("assistant output should not produce a memory candidate")
	}
}

type stubCandidateModel struct {
	response string
	err      error
	calls    int
}

func (m *stubCandidateModel) CompleteText(context.Context, string, string) (string, error) {
	m.calls++
	return m.response, m.err
}

func TestCompositeCandidateExtractorUsesRuleBeforeModel(t *testing.T) {
	model := &stubCandidateModel{err: errors.New("model should not be called")}
	extractor := CompositeCandidateExtractor{
		Primary:  RuleBasedCandidateExtractor{},
		Fallback: AdaptiveCandidateExtractor{Model: model},
	}
	draft, ok, err := extractor.Extract(context.Background(), domain.Message{Role: "user", Content: "I prefer concise answers."})
	if err != nil || !ok || draft.ExtractionReason != CandidateReasonPreference || model.calls != 0 {
		t.Fatalf("rule fast path: draft=%#v ok=%v err=%v calls=%d", draft, ok, err, model.calls)
	}
}

func TestAdaptiveCandidateExtractorReturnsGroundedStructuredDraft(t *testing.T) {
	model := &stubCandidateModel{response: "```json\n{\"decision\":\"add\",\"kind\":\"project_convention\",\"content\":\"The backend uses Go 1.26.5.\",\"confidence\":0.93}\n```"}
	extractor := AdaptiveCandidateExtractor{Model: model}
	draft, ok, err := extractor.Extract(context.Background(), domain.Message{
		Role: "user", Content: "For all backend work we use Go 1.26.5 even when examples mention older versions.",
	})
	if err != nil || !ok {
		t.Fatalf("adaptive extract: draft=%#v ok=%v err=%v", draft, ok, err)
	}
	if draft.Kind != "project_convention" || draft.Content != "The backend uses Go 1.26.5." || draft.Confidence != 0.93 {
		t.Fatalf("adaptive draft mismatch: %#v", draft)
	}
}

func TestAdaptiveCandidateExtractorSupportsNoopAndPrefilter(t *testing.T) {
	model := &stubCandidateModel{response: `{"decision":"noop","kind":"","content":"","confidence":0}`}
	extractor := AdaptiveCandidateExtractor{Model: model}
	if _, ok, err := extractor.Extract(context.Background(), domain.Message{Role: "user", Content: "Can you explain the current memory implementation?"}); err != nil || ok || model.calls != 0 {
		t.Fatalf("question should be filtered: ok=%v err=%v calls=%d", ok, err, model.calls)
	}
	if _, ok, err := extractor.Extract(context.Background(), domain.Message{Role: "user", Content: "This paragraph describes a one-off implementation detail for review."}); err != nil || ok || model.calls != 1 {
		t.Fatalf("model noop: ok=%v err=%v calls=%d", ok, err, model.calls)
	}
}

func TestAdaptiveCandidateExtractorRejectsInvalidStructuredOutput(t *testing.T) {
	model := &stubCandidateModel{response: `{"decision":"replace","kind":"fact","content":"unsupported","confidence":0.9}`}
	_, ok, err := (AdaptiveCandidateExtractor{Model: model}).Extract(context.Background(), domain.Message{
		Role: "user", Content: "The project backend uses the latest supported Go release.",
	})
	if err == nil || ok {
		t.Fatalf("unsupported operation should fail: ok=%v err=%v", ok, err)
	}
}

func TestConservativeCandidatePolicyRejectsUnsafeAndTemporaryContent(t *testing.T) {
	policy := ConservativeCandidatePolicy{}
	tests := []struct {
		content string
		reason  string
	}{
		{"my API key is sk-test", PolicyRejectSecret},
		{"use verbose output for this turn", PolicyRejectTemporary},
		{"the task is complete", PolicyRejectTaskOutcome},
	}
	for _, test := range tests {
		decision := policy.Evaluate(domain.Message{Role: "user"}, CandidateDraft{Content: test.content})
		if decision.Accepted || decision.Reason != test.reason {
			t.Fatalf("policy %q: %#v", test.content, decision)
		}
	}
	if decision := policy.Evaluate(domain.Message{Role: "user"}, CandidateDraft{Content: "concise technical explanations"}); !decision.Accepted {
		t.Fatalf("stable preference was rejected: %#v", decision)
	}
	decision := policy.Evaluate(domain.Message{Role: "user"}, CandidateDraft{
		Content: "concise technical explanations", ExtractionReason: CandidateReasonAdaptive, Confidence: 0.5,
	})
	if decision.Accepted || decision.Reason != PolicyRejectConfidence {
		t.Fatalf("low-confidence adaptive candidate was accepted: %#v", decision)
	}
}
