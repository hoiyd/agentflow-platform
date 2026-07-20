package budget

import (
	"math"

	"agentflow-platform/apps/api/internal/domain"
)

func BuildLedger(runID string, limits domain.RuntimeRunBudget, entries []domain.RunUsageEntry) domain.RunUsageLedger {
	ledger := domain.RunUsageLedger{RunID: runID, Budget: limits, Entries: append([]domain.RunUsageEntry(nil), entries...)}
	reservations := map[string]domain.RunUsageEntry{}
	settlements := map[string]domain.RunUsageEntry{}
	for _, entry := range entries {
		switch entry.Kind {
		case domain.UsageModelReservation:
			reservations[entry.OperationID] = entry
		case domain.UsageModelSettlement:
			settlements[entry.OperationID] = entry
		case domain.UsageToolExecution:
			addEntry(&ledger.Totals, entry)
		}
		if ledger.UpdatedAt == nil || entry.Timestamp.After(*ledger.UpdatedAt) {
			timestamp := entry.Timestamp
			ledger.UpdatedAt = &timestamp
		}
	}
	for operationID, reservation := range reservations {
		if settlement, ok := settlements[operationID]; ok {
			addEntry(&ledger.Totals, settlement)
			continue
		}
		addEntry(&ledger.Totals, reservation)
		ledger.Totals.OpenReservations++
	}
	return ledger
}

func Check(limits domain.RuntimeRunBudget, current domain.RunUsageTotals, requested domain.RunUsageTotals, operationID string, purpose domain.RunUsagePurpose) error {
	checks := []struct {
		resource Resource
		limit    int64
		used     int64
		request  int64
	}{
		{ResourceModelCalls, int64(limits.MaxModelCalls), int64(current.ModelCalls), int64(requested.ModelCalls)},
		{ResourcePromptTokens, int64(limits.MaxPromptTokens), int64(current.PromptTokens), int64(requested.PromptTokens)},
		{ResourceCompletionTokens, int64(limits.MaxCompletionTokens), int64(current.CompletionTokens), int64(requested.CompletionTokens)},
		{ResourceTotalTokens, int64(limits.MaxTotalTokens), int64(current.TotalTokens), int64(requested.TotalTokens)},
		{ResourceToolCalls, int64(limits.MaxToolCalls), int64(current.ToolCalls), int64(requested.ToolCalls)},
		{ResourceEstimatedCost, limits.MaxEstimatedCostMicros, current.EstimatedCostMicros, requested.EstimatedCostMicros},
	}
	for _, check := range checks {
		if check.limit > 0 && check.used+check.request > check.limit {
			return &ExceededError{Resource: check.resource, Limit: check.limit, Used: check.used, Requested: check.request, OperationID: operationID, Purpose: purpose}
		}
	}
	return nil
}

func CheckTotals(limits domain.RuntimeRunBudget, totals domain.RunUsageTotals, operationID string, purpose domain.RunUsagePurpose) error {
	return Check(limits, domain.RunUsageTotals{}, totals, operationID, purpose)
}

func EntryTotals(entry domain.RunUsageEntry) domain.RunUsageTotals {
	var totals domain.RunUsageTotals
	addEntry(&totals, entry)
	return totals
}

func addEntry(total *domain.RunUsageTotals, entry domain.RunUsageEntry) {
	total.ModelCalls += entry.ModelCalls
	total.ToolCalls += entry.ToolCalls
	total.PromptTokens += entry.PromptTokens
	total.CompletionTokens += entry.CompletionTokens
	total.TotalTokens += entry.TotalTokens
	total.EstimatedCostMicros += entry.EstimatedCostMicros
}

func maxCompletionTokens(limits domain.RuntimeRunBudget, totals domain.RunUsageTotals, reservation domain.RunUsageEntry) int {
	maxTokens := math.MaxInt
	limited := false
	if limits.MaxCompletionTokens > 0 {
		limited = true
		maxTokens = min(maxTokens, limits.MaxCompletionTokens-(totals.CompletionTokens-reservation.CompletionTokens))
	}
	if limits.MaxTotalTokens > 0 {
		limited = true
		maxTokens = min(maxTokens, limits.MaxTotalTokens-(totals.TotalTokens-reservation.TotalTokens)-reservation.PromptTokens)
	}
	if limits.MaxEstimatedCostMicros > 0 && limits.OutputCostPerMillionTokensMicros > 0 {
		limited = true
		baseCost := totals.EstimatedCostMicros - reservation.EstimatedCostMicros +
			tokenCostMicros(reservation.PromptTokens, limits.InputCostPerMillionTokensMicros)
		remainingMicros := max(int64(0), limits.MaxEstimatedCostMicros-baseCost)
		costTokens := int((remainingMicros * 1_000_000) / limits.OutputCostPerMillionTokensMicros)
		maxTokens = min(maxTokens, costTokens)
	}
	if !limited {
		return 0
	}
	return max(1, maxTokens)
}

func tokenCostMicros(tokens int, perMillionMicros int64) int64 {
	if tokens <= 0 || perMillionMicros <= 0 {
		return 0
	}
	return (int64(tokens)*perMillionMicros + 999_999) / 1_000_000
}

func normalizePurpose(value domain.RunUsagePurpose) domain.RunUsagePurpose {
	switch value {
	case domain.UsagePurposeRouter, domain.UsagePurposeCompaction:
		return value
	default:
		return domain.UsagePurposePrimary
	}
}
