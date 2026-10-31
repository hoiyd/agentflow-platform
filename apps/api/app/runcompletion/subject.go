package runcompletion

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/verification"
)

// Unlike verifier hashes, this binding preserves question whitespace exactly.
func candidateBindingHash(runID, messageID, question, output string) string {
	data, _ := json.Marshal(struct{ RunID, MessageID, Question, Output string }{runID, messageID, question, output})
	return fmt.Sprintf("sha256:%x", sha256.Sum256(data))
}

type candidate struct {
	question, output string
	binding          domain.RunEvent
	events           []domain.RunEvent
}

func boundCandidate(scoped store.WorkspaceStore, run domain.Run) (candidate, error) {
	items, err := scoped.ListRunEvents(run.ID)
	if err != nil {
		return candidate{}, err
	}
	// Earlier Run-local revisions remain in history. Only the last primary
	// receipt and its following web receipt may identify the current candidate.
	bindingIndex := -1
	for i := range items {
		if items[i].Type == domain.EventCitationResolved && items[i].Payload["protocol_version"] == domain.RAGCitationProtocolVersion {
			bindingIndex = i
		}
	}
	if bindingIndex < 0 {
		return candidate{}, ErrNoCandidate
	}
	binding := items[bindingIndex]
	messageID, _ := binding.Payload["message_id"].(string)
	for _, item := range items[bindingIndex:] {
		if item.Type != domain.EventCitationResolved {
			continue
		}
		id, _ := item.Payload["message_id"].(string)
		if id == "" || item.RunID != run.ID || item.ConversationID != run.ConversationID || messageID != id {
			return candidate{}, ErrNoCandidate
		}
	}
	question, hasQuestion := binding.Payload["verification_question"].(string)
	hash, _ := binding.Payload["verification_subject_hash"].(string)
	if !hasQuestion || hash == "" {
		// Legacy events cannot prove the exact completion input, including Resume.
		return candidate{}, ErrNoCandidate
	}
	messages, err := scoped.ListMessages(run.ConversationID)
	if err != nil {
		return candidate{}, err
	}
	for _, message := range messages {
		if message.ID != messageID {
			continue
		}
		if message.Role != "assistant" || message.ConversationID != run.ConversationID || message.WorkspaceID != run.WorkspaceID || strings.TrimSpace(message.Content) == "" || candidateBindingHash(run.ID, message.ID, question, message.Content) != hash {
			return candidate{}, ErrNoCandidate
		}
		return candidate{question: question, output: message.Content, binding: binding, events: items}, nil
	}
	return candidate{}, ErrNoCandidate
}

func validateSubjectEvidence(scoped store.WorkspaceStore, run domain.Run, subject verification.Subject, bound candidate) error {
	evidence, err := scoped.ListVerificationEvidence(run.ID)
	if err != nil {
		return err
	}
	snapshotHash, err := verification.SnapshotHash(run.RuntimeSnapshot)
	if err != nil {
		return err
	}
	sequences := map[string]int64{}
	for _, item := range bound.events {
		switch item.Type {
		case domain.EventVerificationPassed, domain.EventVerificationFailed, domain.EventVerificationBlocked, domain.EventVerificationStale:
			if id, ok := item.Payload["evidence_id"].(string); ok {
				sequences[id] = item.Sequence
			}
		}
	}
	for _, item := range evidence {
		if item.RunID != run.ID || item.SnapshotHash != snapshotHash || item.ContractID != run.CompletionContract.ID || item.ContractVersion != run.CompletionContract.Version {
			return ErrNoCandidate
		}
		known := false
		for _, spec := range run.CompletionContract.Verifiers {
			if item.VerifierID == spec.ID && item.VerifierType == spec.Type && item.VerifierVersion == spec.Version {
				known = true
				break
			}
		}
		if !known {
			return ErrNoCandidate
		}
		// Verification events are best effort. Prefer their durable sequence;
		// otherwise use the result's start time relative to the candidate receipt.
		if sequence, recorded := sequences[item.ID]; recorded {
			if sequence <= bound.binding.Sequence {
				continue
			}
		} else {
			if item.StartedAt.IsZero() || bound.binding.Timestamp.IsZero() {
				return ErrNoCandidate
			}
			if item.StartedAt.Before(bound.binding.Timestamp) {
				continue
			}
		}
		if item.Status != domain.VerificationStale && item.SubjectHash != subject.Hash {
			return ErrNoCandidate
		}
	}
	// The binding is written before verification, so a failed first Evidence
	// append (including a revision with only historical/stale results) is retryable.
	return nil
}
