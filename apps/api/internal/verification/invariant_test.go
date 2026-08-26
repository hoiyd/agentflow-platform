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

func TestRuntimeInvariantSkipsRunsWithoutVerificationContract(t *testing.T) {
	for _, run := range []domain.Run{
		{ID: "run-without-contract", VerificationStatus: domain.VerificationPassed},
		{ID: "run-not-required", CompletionContract: &domain.CompletionContract{ID: "contract-1"}, VerificationStatus: domain.VerificationNotRequired},
	} {
		if failures := CheckRuntimeInvariants(run, nil); len(failures) != 0 {
			t.Fatalf("run %s should not require verification invariants: %#v", run.ID, failures)
		}
	}
}

func TestRuntimeInvariantRejectsDuplicateEvidence(t *testing.T) {
	run := contractedRun(domain.VerificationRunning)
	evidence := verificationEvidence("evidence-1", 1, "subject-1", domain.VerificationPassed)
	failures := CheckRuntimeInvariants(run, []domain.VerificationEvidence{evidence, evidence})
	if len(failures) != 1 || failures[0].Code != "verification_evidence_duplicate" || failures[0].Owner != "verification" {
		t.Fatalf("unexpected failures: %#v", failures)
	}
}

func TestRuntimeInvariantRejectsMixedSubjectsInLatestAttempt(t *testing.T) {
	run := contractedRun(domain.VerificationFailed)
	evidence := []domain.VerificationEvidence{
		verificationEvidence("evidence-1", 2, "subject-a", domain.VerificationPassed),
		verificationEvidence("evidence-2", 2, "subject-b", domain.VerificationFailed),
	}
	failures := CheckRuntimeInvariants(run, evidence)
	if len(failures) != 1 || failures[0].Code != "verification_subject_mismatch" || failures[0].RunID != run.ID {
		t.Fatalf("unexpected failures: %#v", failures)
	}
}

func TestRuntimeInvariantAcceptsFreshLatestEvidence(t *testing.T) {
	run := contractedRun(domain.VerificationPassed)
	stale := verificationEvidence("stale-marker", 2, "subject-new", domain.VerificationStale)
	stale.SupersedesEvidenceID = "evidence-old"
	evidence := []domain.VerificationEvidence{
		verificationEvidence("evidence-old", 1, "subject-old", domain.VerificationPassed),
		stale,
		verificationEvidence("evidence-new", 2, "subject-new", domain.VerificationPassed),
	}
	if failures := CheckRuntimeInvariants(run, evidence); len(failures) != 0 {
		t.Fatalf("fresh latest evidence should be valid: %#v", failures)
	}
}

func contractedRun(status domain.VerificationStatus) domain.Run {
	return domain.Run{ID: "run-1", CompletionContract: &domain.CompletionContract{ID: "contract-1"}, VerificationStatus: status}
}

func verificationEvidence(id string, attempt int, subject string, status domain.VerificationStatus) domain.VerificationEvidence {
	return domain.VerificationEvidence{ID: id, RunID: "run-1", Attempt: attempt, SubjectHash: subject, Status: status}
}
