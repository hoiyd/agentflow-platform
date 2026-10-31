package event

import (
	"encoding/json"

	"agentflow-platform/apps/api/internal/domain"
)

// The persisted aggregate result remains the verifier's decision. Diagnostics
// additionally require matching request/result facts, not a source label alone.
// Only the latest request can be compared with the current aggregate status.
func verificationBoundary(run domain.Run, boundary domain.RunEvent, events []domain.RunEvent, current bool) bool {
	contract := run.CompletionContract
	if contract == nil {
		return false
	}
	var result struct {
		Source             string                    `json:"source"`
		VerificationStatus domain.VerificationStatus `json:"verification_status"`
		SubjectHash        string                    `json:"subject_hash"`
		Attempt            int                       `json:"attempt"`
	}
	data, err := json.Marshal(boundary.Payload)
	if err != nil || json.Unmarshal(data, &result) != nil {
		return false
	}
	switch boundary.Type {
	case domain.EventRunCompleted, domain.EventRunFailed, domain.EventRunWaitingForUser:
		if result.Source != "completion_gate" {
			return false
		}
	case domain.EventRunRevisionRequested:
		if result.VerificationStatus != domain.VerificationFailed && result.VerificationStatus != domain.VerificationBlocked {
			return false
		}
	default:
		return false
	}
	for i := len(events) - 1; i >= 0; i-- {
		item := events[i]
		if item.Type != domain.EventVerificationRequested {
			continue
		}
		var request struct {
			ContractID      string `json:"contract_id"`
			ContractVersion int    `json:"contract_version"`
			ContractHash    string `json:"contract_hash"`
			SubjectHash     string `json:"subject_hash"`
			Attempt         int    `json:"attempt"`
		}
		data, err := json.Marshal(item.Payload)
		if err != nil || json.Unmarshal(data, &request) != nil || item.RunID != run.ID || request.ContractID != contract.ID || request.ContractVersion != contract.Version || request.ContractHash != contract.Hash || request.SubjectHash == "" || request.Attempt < 1 {
			return false
		}
		if boundary.Type == domain.EventRunRevisionRequested && (result.Attempt != request.Attempt || result.SubjectHash != request.SubjectHash) {
			return false
		}
		for _, observed := range events[i+1:] {
			var status domain.VerificationStatus
			switch observed.Type {
			case domain.EventVerificationPassed:
				status = domain.VerificationPassed
			case domain.EventVerificationFailed:
				status = domain.VerificationFailed
			case domain.EventVerificationBlocked:
				status = domain.VerificationBlocked
			default:
				continue
			}
			if observed.RunID != run.ID || current && status != run.VerificationStatus || (boundary.Type == domain.EventRunCompleted) != (status == domain.VerificationPassed) || boundary.Type == domain.EventRunRevisionRequested && status != result.VerificationStatus {
				continue
			}
			var evidence struct {
				domain.VerificationEvidence
				EvidenceID string `json:"evidence_id"`
			}
			data, err := json.Marshal(observed.Payload)
			if err != nil || json.Unmarshal(data, &evidence) != nil || evidence.EvidenceID == "" || evidence.Status != status || evidence.ContractID != request.ContractID || evidence.ContractVersion != request.ContractVersion || evidence.SubjectHash != request.SubjectHash || evidence.Attempt != request.Attempt {
				continue
			}
			for _, spec := range contract.Verifiers {
				if evidence.VerifierID == spec.ID && evidence.VerifierType == spec.Type && evidence.VerifierVersion == spec.Version {
					return true
				}
			}
		}
		return false
	}
	return false
}
