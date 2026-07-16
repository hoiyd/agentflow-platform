package memory

import (
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
		draft, ok := extractor.Extract(domain.Message{Role: "user", Content: test.content})
		if !ok || draft.Kind != test.kind || draft.ExtractionReason != test.reason || draft.Content != test.want {
			t.Fatalf("extract %q: %#v ok=%v", test.content, draft, ok)
		}
	}
	if _, ok := extractor.Extract(domain.Message{Role: "user", Content: "How does AgentFlow work?"}); ok {
		t.Fatal("ordinary chat should not produce a memory candidate")
	}
	if _, ok := extractor.Extract(domain.Message{Role: "assistant", Content: "Remember that this is true."}); ok {
		t.Fatal("assistant output should not produce a memory candidate")
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
}
