package store

import (
	"encoding/json"
	"errors"

	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/eventcatalog"
)

func PrepareRunEvent(event domain.RunEvent, sequence int64, now time.Time) (domain.RunEvent, error) {
	event.ID = strings.TrimSpace(event.ID)
	if event.ID == "" {
		event.ID = NewID("event")
	}
	if event.Type == "" {
		return domain.RunEvent{}, errors.New("run event type is required")
	}
	if event.SchemaVersion == 0 {
		event.SchemaVersion = domain.CurrentRunEventSchemaVersion
	}
	event.Sequence = sequence
	if event.Payload == nil {
		event.Payload = map[string]any{}
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = now
	}
	if err := eventcatalog.ValidateDurableFact(event); err != nil {
		return domain.RunEvent{}, err
	}
	return event, nil
}

func CloneRuntimeSnapshot(snapshot domain.RuntimeSnapshot) *domain.RuntimeSnapshot {
	bytes, _ := json.Marshal(snapshot)
	var cloned domain.RuntimeSnapshot
	_ = json.Unmarshal(bytes, &cloned)
	return &cloned
}

func CloneRuntimeSnapshotValue(snapshot *domain.RuntimeSnapshot) *domain.RuntimeSnapshot {
	if snapshot == nil {
		return nil
	}
	return CloneRuntimeSnapshot(*snapshot)
}

func CloneRun(run domain.Run) domain.Run {
	run.RuntimeSnapshot = CloneRuntimeSnapshotValue(run.RuntimeSnapshot)
	run.CompletionContract = CloneCompletionContract(run.CompletionContract)
	return run
}

func RunBudget(run domain.Run) domain.RuntimeRunBudget {
	if run.RuntimeSnapshot == nil || run.RuntimeSnapshot.RunBudget == nil {
		return domain.RuntimeRunBudget{}
	}
	return *run.RuntimeSnapshot.RunBudget
}

func ValidateUsageEntry(entry domain.RunUsageEntry) error {
	if strings.TrimSpace(entry.ID) == "" || strings.TrimSpace(entry.RunID) == "" || strings.TrimSpace(entry.OperationID) == "" {
		return errors.New("run usage requires id, run_id, and operation_id")
	}
	if entry.Purpose != domain.UsagePurposePrimary && entry.Purpose != domain.UsagePurposeRouter && entry.Purpose != domain.UsagePurposeCompaction {
		return errors.New("run usage purpose is invalid")
	}
	if entry.ModelCalls < 0 || entry.ToolCalls < 0 || entry.PromptTokens < 0 || entry.CompletionTokens < 0 || entry.TotalTokens < 0 || entry.EstimatedCostMicros < 0 {
		return errors.New("run usage values cannot be negative")
	}
	switch entry.Kind {
	case domain.UsageModelReservation, domain.UsageModelSettlement:
		if entry.ModelCalls != 1 || entry.ToolCalls != 0 || entry.TotalTokens < entry.PromptTokens+entry.CompletionTokens {
			return errors.New("model usage entry has invalid counters")
		}
	case domain.UsageToolExecution:
		if entry.ToolCalls != 1 || entry.ModelCalls != 0 || entry.PromptTokens != 0 || entry.CompletionTokens != 0 || entry.TotalTokens != 0 || entry.EstimatedCostMicros != 0 {
			return errors.New("tool usage entry has invalid counters")
		}
	default:
		return errors.New("run usage kind is invalid")
	}
	if entry.Timestamp.IsZero() {
		return errors.New("run usage timestamp is required")
	}
	return nil
}

func HasUsageReservation(entries []domain.RunUsageEntry, operationID string) bool {
	for _, entry := range entries {
		if entry.OperationID == operationID && entry.Kind == domain.UsageModelReservation {
			return true
		}
	}
	return false
}

func SameUsageEntry(left, right domain.RunUsageEntry) bool {
	left.ID, right.ID = "", ""
	left.Timestamp, right.Timestamp = time.Time{}, time.Time{}
	return left == right
}

func CloneCompletionContract(contract *domain.CompletionContract) *domain.CompletionContract {
	if contract == nil {
		return nil
	}
	bytes, _ := json.Marshal(contract)
	var cloned domain.CompletionContract
	_ = json.Unmarshal(bytes, &cloned)
	return &cloned
}

func VerificationEvidenceForRun(items []domain.VerificationEvidence, runID string) []domain.VerificationEvidence {
	result := []domain.VerificationEvidence{}
	for _, item := range items {
		if item.RunID == runID {
			result = append(result, CloneVerificationEvidence(item))
		}
	}
	return result
}

func CloneVerificationEvidence(evidence domain.VerificationEvidence) domain.VerificationEvidence {
	evidence.ArtifactIDs = append([]string(nil), evidence.ArtifactIDs...)
	if evidence.Details == nil {
		evidence.Details = map[string]any{}
	} else {
		encoded, _ := json.Marshal(evidence.Details)
		evidence.Details = nil
		_ = json.Unmarshal(encoded, &evidence.Details)
	}
	return evidence
}

func VerificationArtifactsForRun(items []domain.VerificationArtifact, runID string) []domain.VerificationArtifact {
	result := []domain.VerificationArtifact{}
	for _, item := range items {
		if item.RunID == runID {
			result = append(result, item)
		}
	}
	return result
}
