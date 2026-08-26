package projection

import (
	"encoding/json"
	"sort"

	"agentflow-platform/apps/api/internal/domain"
)

func BuildSnapshot(run domain.Run, events []domain.RunEvent, ledger domain.RunUsageLedger, evidence []domain.VerificationEvidence) domain.RunProjectionSnapshot {
	watermark := eventWatermark(events)
	return domain.RunProjectionSnapshot{
		Run:               BuildRunProjection(run, events),
		Usage:             BuildUsageProjection(ledger, watermark),
		Verification:      BuildVerificationProjection(run, evidence, watermark),
		AsOfSequence:      watermark,
		InvariantFailures: []domain.RuntimeInvariantFailure{},
	}
}

func BuildRunProjection(run domain.Run, events []domain.RunEvent) domain.RunProjection {
	stages := map[string]bool{}
	turns := map[string]bool{}
	models := map[string]bool{}
	tools := map[string]bool{}
	for _, item := range events {
		switch item.Type {
		case domain.EventStageStarted:
			setPresent(stages, item.StageID, true)
		case domain.EventStageCompleted, domain.EventStageFailed, domain.EventStageCanceled:
			setPresent(stages, item.StageID, false)
		case domain.EventTurnStarted:
			setPresent(turns, item.TurnID, true)
		case domain.EventTurnCompleted, domain.EventTurnFailed, domain.EventTurnCanceled:
			setPresent(turns, item.TurnID, false)
		case domain.EventModelStarted:
			setPresent(models, modelCallID(item), true)
		case domain.EventModelCompleted, domain.EventModelFailed:
			setPresent(models, modelCallID(item), false)
		case domain.EventToolStarted:
			setPresent(tools, stringPayload(item.Payload, "tool_call_id"), true)
		case domain.EventToolCompleted, domain.EventToolFailed:
			setPresent(tools, stringPayload(item.Payload, "tool_call_id"), false)
		}
	}
	return domain.RunProjection{
		RunID: run.ID, ConversationID: run.ConversationID, Status: run.Status,
		VerificationStatus: run.VerificationStatus,
		ActiveStageIDs:     sortedKeys(stages), ActiveTurnIDs: sortedKeys(turns),
		ActiveModelCallIDs: sortedKeys(models), ActiveToolCallIDs: sortedKeys(tools),
		Summary: BuildRunTraceSummary(run, events), AsOfSequence: eventWatermark(events),
	}
}

// ConsumesRunEvent is the projection coverage contract checked against the
// Event Catalog in CI. Some events affect active scopes directly; others feed
// the trace summary or confirm the authoritative Run status.
func ConsumesRunEvent(eventType domain.RunEventType) bool {
	switch eventType {
	case domain.EventRunCreated, domain.EventRunStarted, domain.EventRunWaitingForUser,
		domain.EventRunResumed, domain.EventRunCancelRequested, domain.EventRunCanceled,
		domain.EventRunCompleted, domain.EventRunFailed, domain.EventRunRevisionRequested,
		domain.EventStageStarted, domain.EventStageCompleted, domain.EventStageFailed, domain.EventStageCanceled,
		domain.EventTurnStarted, domain.EventTurnCompleted, domain.EventTurnFailed, domain.EventTurnCanceled,
		domain.EventModelStarted, domain.EventModelCompleted, domain.EventModelFailed,
		domain.EventToolStarted, domain.EventToolCompleted, domain.EventToolFailed,
		domain.EventRetrievalFailed, domain.EventHistorySearchFailed, domain.EventCompactionFailed,
		domain.EventMemoryCandidateFailed, domain.EventMemorySyncFailed, domain.EventBudgetExceeded:
		return true
	default:
		return false
	}
}

func BuildUsageProjection(ledger domain.RunUsageLedger, watermark int64) domain.UsageProjection {
	return domain.UsageProjection{Ledger: ledger, AsOfSequence: watermark}
}

func BuildVerificationProjection(run domain.Run, evidence []domain.VerificationEvidence, watermark int64) domain.VerificationProjection {
	result := domain.VerificationProjection{
		Status: run.VerificationStatus, EvidenceCount: len(evidence), AsOfSequence: watermark,
	}
	stale := map[string]bool{}
	var latest *domain.VerificationEvidence
	for index := range evidence {
		item := &evidence[index]
		if item.Status == domain.VerificationStale && item.SupersedesEvidenceID != "" {
			stale[item.SupersedesEvidenceID] = true
		}
		if item.Attempt > result.LatestAttempt {
			result.LatestAttempt = item.Attempt
		}
		if item.Status != domain.VerificationStale && (latest == nil || item.CompletedAt.After(latest.CompletedAt) ||
			(item.CompletedAt.Equal(latest.CompletedAt) && item.ID > latest.ID)) {
			latest = item
		}
	}
	if latest == nil {
		return result
	}
	result.CurrentSubjectHash = latest.SubjectHash
	for _, item := range evidence {
		if item.Status != domain.VerificationStale && !stale[item.ID] && item.SubjectHash == latest.SubjectHash {
			result.FreshEvidenceCount++
		}
	}
	return result
}

func BuildRunTraceSummary(run domain.Run, events []domain.RunEvent) domain.RunTraceSummary {
	summary := domain.RunTraceSummary{RunID: run.ID, Status: run.Status}
	if run.StartedAt != nil {
		end := run.UpdatedAt
		if run.CompletedAt != nil {
			end = *run.CompletedAt
		}
		if end.IsZero() {
			end = *run.StartedAt
		}
		if end.After(*run.StartedAt) {
			summary.TotalDurationMS = end.Sub(*run.StartedAt).Milliseconds()
		}
	}
	for _, item := range events {
		switch item.Type {
		case domain.EventModelCompleted:
			summary.LLMCalls++
			summary.PromptTokens += intPayload(item.Payload, "prompt_tokens")
			summary.CompletionTokens += intPayload(item.Payload, "completion_tokens")
			summary.TotalTokens += intPayload(item.Payload, "total_tokens")
			if boolPayload(item.Payload, "token_usage_estimated") {
				summary.TokenUsageEstimated = true
			}
		case domain.EventToolCompleted:
			summary.ToolCalls++
		case domain.EventToolFailed:
			summary.ToolCalls++
			summary.ErrorCount++
		case domain.EventModelFailed, domain.EventRetrievalFailed, domain.EventHistorySearchFailed,
			domain.EventCompactionFailed, domain.EventMemoryCandidateFailed, domain.EventMemorySyncFailed,
			domain.EventBudgetExceeded:
			summary.ErrorCount++
		}
	}
	return summary
}

func eventWatermark(events []domain.RunEvent) int64 {
	var watermark int64
	for _, item := range events {
		if item.Sequence > watermark {
			watermark = item.Sequence
		}
	}
	return watermark
}

func setPresent(items map[string]bool, id string, present bool) {
	if id == "" {
		return
	}
	if present {
		items[id] = true
		return
	}
	delete(items, id)
}

func sortedKeys(items map[string]bool) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func modelCallID(item domain.RunEvent) string {
	if id := stringPayload(item.Payload, "model_call_id"); id != "" {
		return id
	}
	return item.TurnID
}

func stringPayload(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return value
}

func intPayload(payload map[string]any, key string) int {
	switch value := payload[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		i, _ := value.Int64()
		return int(i)
	default:
		return 0
	}
}

func boolPayload(payload map[string]any, key string) bool {
	value, ok := payload[key].(bool)
	return ok && value
}
