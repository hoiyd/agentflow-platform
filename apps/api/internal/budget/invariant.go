package budget

import (
	"fmt"

	"agentflow-platform/apps/api/internal/domain"
)

func CheckRuntimeInvariants(runID string, entries []domain.RunUsageEntry) []domain.RuntimeInvariantFailure {
	seen := map[string]domain.RunUsageEntry{}
	reservations := map[string]bool{}
	failures := []domain.RuntimeInvariantFailure{}
	for _, item := range entries {
		key := item.OperationID + "\x00" + string(item.Kind)
		if previous, ok := seen[key]; ok {
			code := "usage_duplicate_entry"
			if item.Kind == domain.UsageModelSettlement {
				code = "usage_duplicate_settlement"
			}
			failures = append(failures, domain.RuntimeInvariantFailure{
				Code: code, Owner: "budget", RunID: runID,
				Message: fmt.Sprintf("usage operation %s has duplicate %s entries (%s, %s)", item.OperationID, item.Kind, previous.ID, item.ID),
			})
		} else {
			seen[key] = item
		}
		if item.Kind == domain.UsageModelReservation {
			reservations[item.OperationID] = true
		}
	}
	for _, item := range entries {
		if item.Kind == domain.UsageModelSettlement && !reservations[item.OperationID] {
			failures = append(failures, domain.RuntimeInvariantFailure{
				Code: "usage_settlement_without_reservation", Owner: "budget", RunID: runID,
				Message: fmt.Sprintf("usage settlement %s has no reservation", item.OperationID),
			})
		}
	}
	return failures
}
