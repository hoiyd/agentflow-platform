package verification

import (
	"fmt"

	"agentflow-platform/apps/api/internal/domain"
)

func CheckRuntimeInvariants(run domain.Run, evidence []domain.VerificationEvidence) []domain.RuntimeInvariantFailure {
	if run.CompletionContract == nil || run.VerificationStatus == domain.VerificationNotRequired {
		return []domain.RuntimeInvariantFailure{}
	}
	seen := map[string]bool{}
	stale := map[string]bool{}
	for _, item := range evidence {
		if seen[item.ID] {
			return []domain.RuntimeInvariantFailure{{
				Code: "verification_evidence_duplicate", Owner: "verification", RunID: run.ID,
				Message: fmt.Sprintf("verification evidence %s is duplicated", item.ID),
			}}
		}
		seen[item.ID] = true
		if item.Status == domain.VerificationStale && item.SupersedesEvidenceID != "" {
			stale[item.SupersedesEvidenceID] = true
		}
	}
	latestAttempt := 0
	latestSubject := ""
	for _, item := range evidence {
		if item.Status != domain.VerificationStale && !stale[item.ID] && item.Attempt >= latestAttempt {
			latestAttempt = item.Attempt
			latestSubject = item.SubjectHash
		}
	}
	terminal := run.VerificationStatus == domain.VerificationPassed || run.VerificationStatus == domain.VerificationFailed || run.VerificationStatus == domain.VerificationBlocked
	if terminal && latestSubject == "" {
		return []domain.RuntimeInvariantFailure{{
			Code: "verification_fresh_evidence_missing", Owner: "verification", RunID: run.ID,
			Message: "terminal verification status has no fresh evidence",
		}}
	}
	for _, item := range evidence {
		if item.Status == domain.VerificationStale || stale[item.ID] || item.Attempt != latestAttempt {
			continue
		}
		if item.SubjectHash != latestSubject {
			return []domain.RuntimeInvariantFailure{{
				Code: "verification_subject_mismatch", Owner: "verification", RunID: run.ID,
				Message: fmt.Sprintf("verification attempt %d contains evidence for multiple subjects", latestAttempt),
			}}
		}
	}
	return []domain.RuntimeInvariantFailure{}
}
