package verification

import (
	"encoding/json"
	"slices"

	"agentflow-platform/apps/api/internal/domain"
)

// StructuredResult is shared by Replay and script reads (SSE links to the API). Never take the
// latest Conversation answer: it may belong to another Run or a failed revision.
func StructuredResult(run domain.Run, evidence []domain.VerificationEvidence, artifacts []domain.VerificationArtifact) *domain.StructuredRunResult {
	if run.CompletionContract == nil || run.CompletionContract.Output == nil {
		return nil
	}
	contract := run.CompletionContract
	output := contract.Output
	result := &domain.StructuredRunResult{RunID: run.ID, Status: run.Status, VerificationStatus: run.VerificationStatus, ContractHash: contract.Hash, Version: output.Version, Mode: output.Mode, VerifierID: output.VerifierID}
	if err := ValidateFrozenContract(contract); err != nil {
		result.UnavailableReason = "contract_invalid"
		return result
	}
	result.UnavailableReason = "run_not_completed"
	if run.Status != domain.RunCompleted {
		return result
	}
	result.UnavailableReason = "verification_not_passed"
	if run.VerificationStatus != domain.VerificationPassed {
		return result
	}
	result.UnavailableReason = "evidence_unavailable"
	latest := 0
	for _, e := range evidence {
		if e.RunID == run.ID && e.Attempt > latest {
			latest = e.Attempt
		}
	}
	for _, e := range evidence {
		if e.RunID != run.ID || e.Attempt != latest || e.VerifierID != output.VerifierID || e.VerifierType != domain.VerifierJSONSchema || e.Status != domain.VerificationPassed || e.ContractID != contract.ID || e.ContractVersion != contract.Version {
			continue
		}
		validRevision := false
		for _, spec := range contract.Verifiers {
			if spec.ID == e.VerifierID && spec.Required && spec.Version == e.VerifierVersion {
				validRevision = true
			}
		}
		if !validRevision {
			continue
		}
		result.EvidenceID, result.SubjectHash = e.ID, e.SubjectHash
		result.UnavailableReason = "artifact_unavailable"
		for _, a := range artifacts {
			if a.RunID != run.ID || a.EvidenceID != e.ID || !slices.Contains(e.ArtifactIDs, a.ID) || a.Kind != "subject" || a.MediaType != "application/json" {
				continue
			}
			result.ArtifactID = a.ID
			if a.Truncated || len(a.Content) > domain.MaxStructuredOutputBytes || a.ContentHash != hashBytes([]byte(a.Content)) {
				result.UnavailableReason = "artifact_modified"
				return result
			}
			if !json.Valid([]byte(a.Content)) {
				result.UnavailableReason = "invalid_json"
				return result
			}
			result.Value = json.RawMessage(a.Content)
			result.UnavailableReason = ""
			return result
		}
	}
	return result
}
