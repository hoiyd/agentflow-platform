package verification

import (
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestRuntimeInvariantRequiresFreshEvidenceForTerminalStatus(t *testing.T) {
	run := domain.Run{ID: "run-1", CompletionContract: &domain.CompletionContract{ID: "contract-1"}, VerificationStatus: domain.VerificationPassed}
	failures := CheckRuntimeInvariants(run, nil)
	if len(failures) != 1 || failures[0].Code != "verification_fresh_evidence_missing" {
		t.Fatalf("unexpected failures: %#v", failures)
	}
}
