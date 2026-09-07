package projection

import (
	"fmt"
	"sort"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

const maxRecoveryItems = 20

// BuildRecoverySummary explains a stopped Run using records already present
// in Replay. A nil result keeps healthy and active Runs free of recovery noise.
func BuildRecoverySummary(replay domain.RunReplay) *domain.RecoverySummary {
	effects := unresolvedToolEffects(replay.ToolEffects)
	blockers, artifactRefs := latestTaskStateSignals(replay.TaskStateRevisions)
	blockedChildren := blockedChildDelegations(replay.ChildDelegations)

	summary := recoveryReason(replay, effects, blockers, blockedChildren)
	if summary == nil {
		return nil
	}
	summary.Evidence = recoveryEvidence(replay, effects, blockers, blockedChildren)
	summary.ArtifactRefs = appendUnique(artifactRefs, delegationOutputRefs(blockedChildren)...)
	summary.Actions = recoveryActions(replay, len(effects) > 0, len(blockers) > 0, blockedChildren)
	return summary
}

func recoveryReason(replay domain.RunReplay, effects []domain.ToolEffectSummary, blockers []domain.TaskBlocker, children []domain.RunDelegation) *domain.RecoverySummary {
	if len(effects) > 0 {
		return newRecoverySummary(domain.RecoveryToolEffectUncertain, "Tool effect needs reconciliation", "An external side effect has an uncertain outcome. Resolve it before resuming this run.")
	}
	if replay.ParentDelegation != nil && replay.Run.Status == domain.RunFailedRecoverable {
		return newRecoverySummary(domain.RecoveryChildOwnedByParent, "Child run stopped", "This child run is recoverable through its parent run, which owns the delegation stage.")
	}
	if len(children) > 0 {
		return newRecoverySummary(domain.RecoveryChildBlocked, "Child run blocked the parent", "A delegated child run did not finish. Resume the parent after reviewing the child evidence.")
	}
	if replay.Run.VerificationStatus == domain.VerificationFailed {
		return newRecoverySummary(domain.RecoveryVerificationFailed, "Verification failed", "The candidate output did not satisfy its completion contract.")
	}
	if replay.Run.VerificationStatus == domain.VerificationBlocked {
		return newRecoverySummary(domain.RecoveryVerificationBlocked, "Verification blocked", "The completion contract could not be evaluated with the available evidence.")
	}
	if len(blockers) > 0 && (replay.Run.Status == domain.RunWaitingForUser || replay.Run.Status == domain.RunFailed || replay.Run.Status == domain.RunFailedRecoverable) {
		return newRecoverySummary(domain.RecoveryTaskBlocked, "Task is blocked", "Structured task state records unresolved blockers for this conversation.")
	}
	switch replay.Run.Status {
	case domain.RunFailedRecoverable:
		return newRecoverySummary(domain.RecoveryRunRecoverable, "Run can be resumed", "The run stopped unexpectedly and has a durable recovery point.")
	case domain.RunWaitingForUser:
		return newRecoverySummary(domain.RecoveryInputRequired, "Input required", "The run is waiting for user input before it can continue.")
	case domain.RunFailed:
		return newRecoverySummary(domain.RecoveryRunFailed, "Run failed", "The run ended with a non-recoverable failure.")
	case domain.RunCanceled:
		return newRecoverySummary(domain.RecoveryRunCanceled, "Run canceled", "The run was canceled and cannot be resumed.")
	default:
		return nil
	}
}

func newRecoverySummary(reason domain.RecoveryReason, title, message string) *domain.RecoverySummary {
	return &domain.RecoverySummary{Reason: reason, Title: title, Message: message, Evidence: []domain.RecoveryEvidence{}, ArtifactRefs: []string{}, Actions: []domain.RecoveryAction{}}
}

func recoveryEvidence(replay domain.RunReplay, effects []domain.ToolEffectSummary, blockers []domain.TaskBlocker, children []domain.RunDelegation) []domain.RecoveryEvidence {
	items := make([]domain.RecoveryEvidence, 0, maxRecoveryItems)
	if message := strings.TrimSpace(replay.Run.Error); message != "" {
		items = append(items, domain.RecoveryEvidence{Kind: "run_error", ID: replay.Run.ID, Status: string(replay.Run.Status), Summary: message})
	}
	for _, effect := range effects {
		items = appendRecoveryEvidence(items, domain.RecoveryEvidence{Kind: "tool_effect", ID: effect.IdempotencyKey, Status: string(effect.Status), Summary: fmt.Sprintf("%s may have changed external state", effect.ToolName)})
	}
	for _, item := range replay.VerificationEvidence {
		if item.Status != domain.VerificationFailed && item.Status != domain.VerificationBlocked {
			continue
		}
		items = appendRecoveryEvidence(items, domain.RecoveryEvidence{Kind: "verification", ID: item.ID, Status: string(item.Status), Summary: item.Summary, ArtifactRefs: append([]string(nil), item.ArtifactIDs...)})
	}
	for _, blocker := range blockers {
		items = appendRecoveryEvidence(items, domain.RecoveryEvidence{Kind: "task_blocker", ID: blocker.ID, Status: string(blocker.Status), Summary: blocker.Description})
	}
	for _, child := range children {
		summary := strings.TrimSpace(child.Error)
		if summary == "" {
			summary = "Child run requires review"
		}
		refs := []string{}
		if child.OutputRef != "" {
			refs = append(refs, child.OutputRef)
		}
		items = appendRecoveryEvidence(items, domain.RecoveryEvidence{Kind: "child_run", ID: child.ChildRunID, Status: string(child.Status), Summary: summary, ArtifactRefs: refs})
	}
	if replay.ParentDelegation != nil && replay.Run.Status == domain.RunFailedRecoverable {
		items = appendRecoveryEvidence(items, domain.RecoveryEvidence{Kind: "parent_run", ID: replay.ParentDelegation.ParentRunID, Status: string(replay.ParentDelegation.Status), Summary: "The parent run owns recovery for this delegated stage"})
	}
	return items
}

func recoveryActions(replay domain.RunReplay, hasUnresolvedEffect, hasBlockers bool, children []domain.RunDelegation) []domain.RecoveryAction {
	actions := []domain.RecoveryAction{}
	if hasUnresolvedEffect {
		actions = append(actions, domain.RecoveryAction{Kind: "reconcile_tool_effect", Label: "Review tool effects", Enabled: true, TargetID: replay.Run.ID})
	}
	if replay.Run.Status == domain.RunFailedRecoverable && replay.ParentDelegation == nil {
		action := domain.RecoveryAction{Kind: "resume_run", Label: "Resume run", Enabled: !hasUnresolvedEffect, TargetID: replay.Run.ID}
		if !action.Enabled {
			action.UnavailableReason = "Resolve uncertain tool effects before resuming"
		}
		actions = append(actions, action)
	}
	if replay.Run.Status == domain.RunWaitingForUser {
		action := domain.RecoveryAction{Kind: "continue_in_chat", Label: "Continue in chat", Enabled: !hasUnresolvedEffect, TargetID: replay.Run.ConversationID}
		if !action.Enabled {
			action.UnavailableReason = "Resolve uncertain tool effects before continuing"
		}
		actions = append(actions, action)
	}
	if replay.ParentDelegation != nil && replay.Run.Status == domain.RunFailedRecoverable {
		actions = append(actions, domain.RecoveryAction{Kind: "inspect_parent_run", Label: "Open parent run", Enabled: true, TargetID: replay.ParentDelegation.ParentRunID})
	}
	for _, child := range children {
		actions = append(actions, domain.RecoveryAction{Kind: "inspect_child_run", Label: "Open child run", Enabled: true, TargetID: child.ChildRunID})
	}
	if replay.Run.VerificationStatus == domain.VerificationFailed || replay.Run.VerificationStatus == domain.VerificationBlocked {
		action := domain.RecoveryAction{Kind: "review_verification", Label: "Review verification", Enabled: hasFailedVerificationEvidence(replay.VerificationEvidence)}
		if !action.Enabled {
			action.UnavailableReason = "No verification evidence was recorded"
		}
		actions = append(actions, action)
	}
	if hasBlockers {
		actions = append(actions, domain.RecoveryAction{Kind: "review_task_state", Label: "Review task state", Enabled: true})
	}
	return actions
}

func hasFailedVerificationEvidence(evidence []domain.VerificationEvidence) bool {
	for _, item := range evidence {
		if item.Status == domain.VerificationFailed || item.Status == domain.VerificationBlocked {
			return true
		}
	}
	return false
}

func unresolvedToolEffects(effects []domain.ToolEffectSummary) []domain.ToolEffectSummary {
	items := make([]domain.ToolEffectSummary, 0)
	for _, effect := range effects {
		if domain.ToolEffectRequiresReconciliation(effect.Status) {
			items = append(items, effect)
		}
	}
	return items
}

func latestTaskStateSignals(revisions []domain.TaskStateRevision) ([]domain.TaskBlocker, []string) {
	if len(revisions) == 0 {
		return nil, nil
	}
	latest := revisions[0]
	for _, revision := range revisions[1:] {
		if revision.Version > latest.Version {
			latest = revision
		}
	}
	blockers := []domain.TaskBlocker{}
	for _, blocker := range latest.State.Blockers {
		if blocker.Status == domain.TaskBlockerOpen {
			blockers = append(blockers, blocker)
		}
	}
	refs := append([]string(nil), latest.State.ArtifactRefs...)
	for _, task := range latest.State.Tasks {
		refs = appendUnique(refs, task.ArtifactRefs...)
	}
	return blockers, refs
}

func blockedChildDelegations(items []domain.RunDelegation) []domain.RunDelegation {
	result := []domain.RunDelegation{}
	for _, item := range items {
		if item.Status == domain.DelegationBlocked || item.Status == domain.DelegationFailed {
			result = append(result, item)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ChildRunID < result[j].ChildRunID })
	if len(result) > maxRecoveryItems {
		result = result[:maxRecoveryItems]
	}
	return result
}

func delegationOutputRefs(items []domain.RunDelegation) []string {
	refs := []string{}
	for _, item := range items {
		if item.OutputRef != "" {
			refs = appendUnique(refs, item.OutputRef)
		}
	}
	return refs
}

func appendRecoveryEvidence(items []domain.RecoveryEvidence, item domain.RecoveryEvidence) []domain.RecoveryEvidence {
	if len(items) >= maxRecoveryItems {
		return items
	}
	return append(items, item)
}

func appendUnique(items []string, values ...string) []string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		found := false
		for _, existing := range items {
			if existing == value {
				found = true
				break
			}
		}
		if !found && len(items) < maxRecoveryItems {
			items = append(items, value)
		}
	}
	return items
}
