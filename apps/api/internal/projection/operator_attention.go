package projection

import (
	"sort"

	"agentflow-platform/apps/api/internal/domain"
)

// BuildOperatorAttention derives the highest-priority operator concern for a
// Run from the same Recovery Summary shown by Replay.
func BuildOperatorAttention(replay domain.RunReplay) *domain.OperatorAttentionItem {
	summary := replay.RecoverySummary
	if summary == nil {
		summary = BuildRecoverySummary(replay)
	}
	if summary == nil {
		return nil
	}
	reason := attentionReason(summary.Reason)
	if reason == "" {
		return nil
	}
	return &domain.OperatorAttentionItem{
		RunID: replay.Run.ID, ConversationID: replay.Run.ConversationID,
		ConversationTitle: replay.Conversation.Title, RunStatus: replay.Run.Status,
		Reason: reason, Title: summary.Title, Message: summary.Message,
		Evidence:            append([]domain.RecoveryEvidence(nil), summary.Evidence...),
		RecommendedAction:   recommendedRecoveryAction(summary.Reason, summary.Actions),
		ObservationSequence: eventWatermark(replay.RunEvents), UpdatedAt: replay.Run.UpdatedAt,
	}
}

func BuildOperatorAttentionList(replays []domain.RunReplay) []domain.OperatorAttentionItem {
	items := make([]domain.OperatorAttentionItem, 0, len(replays))
	for _, replay := range replays {
		if item := BuildOperatorAttention(replay); item != nil {
			items = append(items, *item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		left, right := attentionPriority(items[i].Reason), attentionPriority(items[j].Reason)
		if left != right {
			return left < right
		}
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.Before(items[j].UpdatedAt)
		}
		return items[i].RunID < items[j].RunID
	})
	return items
}

func attentionReason(reason domain.RecoveryReason) domain.OperatorAttentionReason {
	switch reason {
	case domain.RecoveryToolEffectUncertain:
		return domain.AttentionReconciliationRequired
	case domain.RecoveryRunRecoverable:
		return domain.AttentionRecoveryAvailable
	case domain.RecoveryInputRequired, domain.RecoveryTaskBlocked:
		return domain.AttentionWaitingForUser
	case domain.RecoveryVerificationFailed, domain.RecoveryVerificationBlocked:
		return domain.AttentionVerification
	case domain.RecoveryBudgetExhausted:
		return domain.AttentionBudgetExhausted
	case domain.RecoveryRunFailed:
		return domain.AttentionFailure
	default:
		return ""
	}
}

func recommendedRecoveryAction(reason domain.RecoveryReason, actions []domain.RecoveryAction) *domain.RecoveryAction {
	var want string
	switch reason {
	case domain.RecoveryToolEffectUncertain:
		want = "reconcile_tool_effect"
	case domain.RecoveryRunRecoverable:
		want = "resume_run"
	case domain.RecoveryInputRequired:
		want = "continue_in_chat"
	case domain.RecoveryTaskBlocked:
		want = "review_task_state"
	case domain.RecoveryVerificationFailed, domain.RecoveryVerificationBlocked:
		want = "review_verification"
	}
	for index := range actions {
		if actions[index].Kind == want {
			action := actions[index]
			return &action
		}
	}
	return nil
}

func attentionPriority(reason domain.OperatorAttentionReason) int {
	switch reason {
	case domain.AttentionReconciliationRequired:
		return 0
	case domain.AttentionRecoveryAvailable:
		return 1
	case domain.AttentionWaitingForUser:
		return 2
	case domain.AttentionVerification:
		return 3
	case domain.AttentionBudgetExhausted:
		return 4
	default:
		return 5
	}
}
