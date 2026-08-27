package recovery

import (
	"fmt"
	"log"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
)

const staleRunMessage = "run heartbeat expired; previous worker may have crashed"

type Store interface {
	ListStaleRunningRuns(time.Time) ([]domain.Run, error)
	ListRunEvents(string) ([]domain.RunEvent, error)
	RepairInterruptedRun(domain.InterruptedRunRepair) (domain.InterruptedRunRepairResult, error)
}

type DelegationStore interface {
	ListActiveRunDelegations() ([]domain.RunDelegation, error)
	GetRun(string) (domain.Run, bool, error)
	ListCollaborationSteps(string) ([]domain.CollaborationStep, error)
	UpdateCollaborationStep(string, domain.CollaborationStepStatus, string, string) (domain.CollaborationStep, error)
	UpdateRunDelegation(string, domain.DelegationResult) (domain.RunDelegation, error)
	CreateRunEvent(domain.RunEvent) (domain.RunEvent, error)
}

// MarkStaleRunningRuns repairs durable lifecycle scopes before exposing a stale
// Run as recoverable. The store re-checks staleness and commits both changes
// atomically, making repeated startup scans idempotent.
func MarkStaleRunningRuns(appStore Store, staleTimeout time.Duration) (int, error) {
	if staleTimeout <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-staleTimeout)
	runs, err := appStore.ListStaleRunningRuns(cutoff)
	if err != nil {
		return 0, err
	}
	repaired := 0
	for _, run := range runs {
		events, err := appStore.ListRunEvents(run.ID)
		if err != nil {
			return repaired, err
		}
		terminal, err := event.PlanInterruptedLifecycleRepair(events, domain.InterruptedWorkerReason)
		if err != nil {
			return repaired, fmt.Errorf("plan interrupted run repair %s: %w", run.ID, err)
		}
		var cursor int64
		if len(events) > 0 {
			cursor = events[len(events)-1].Sequence
		}
		result, err := appStore.RepairInterruptedRun(domain.InterruptedRunRepair{
			RunID: run.ID, StaleBefore: cutoff, ExpectedEventCursor: cursor,
			TerminalEvents: terminal, ErrorMessage: staleRunMessage,
		})
		if err != nil {
			return repaired, err
		}
		if !result.Applied {
			continue
		}
		combined := append(append([]domain.RunEvent(nil), events...), result.AppendedEvents...)
		if err := event.ValidateLifecycle(combined); err != nil {
			return repaired, fmt.Errorf("validate repaired run %s: %w", run.ID, err)
		}
		repaired++
		log.Printf("native_recovery_repaired run_id=%s synthetic_events=%d heartbeat_at=%v cutoff=%s", run.ID, len(result.AppendedEvents), run.HeartbeatAt, cutoff.Format(time.RFC3339))
	}
	return repaired, nil
}

// ReconcileChildRunDelegations closes or blocks the durable parent-child protocol
// after stale child Runs have been repaired. It is idempotent because only
// created/running relations are considered. A child that completed before a
// crash has its bounded result reconstructed from its own durable stage.
func ReconcileChildRunDelegations(appStore DelegationStore) (int, error) {
	items, err := appStore.ListActiveRunDelegations()
	if err != nil {
		return 0, err
	}
	reconciled := 0
	for _, item := range items {
		child, ok, err := appStore.GetRun(item.ChildRunID)
		if err != nil {
			return reconciled, err
		}
		if !ok {
			return reconciled, fmt.Errorf("delegation %s child run %s not found", item.ID, item.ChildRunID)
		}
		result, eventType, terminal := domain.DelegationResult{}, domain.RunEventType(""), false
		switch child.Status {
		case domain.RunCompleted:
			steps, err := appStore.ListCollaborationSteps(child.ID)
			if err != nil {
				return reconciled, err
			}
			for index := len(steps) - 1; index >= 0; index-- {
				if steps[index].Status == domain.CollaborationStepCompleted {
					maxChars := 4000
					if child.RuntimeSnapshot != nil && child.RuntimeSnapshot.Delegation != nil && child.RuntimeSnapshot.Delegation.SummaryMaxChars > 0 {
						maxChars = child.RuntimeSnapshot.Delegation.SummaryMaxChars
					}
					result = domain.CompletedDelegationResult(child.ID, steps[index], maxChars)
					_, err = appStore.UpdateCollaborationStep(item.ParentStageID, domain.CollaborationStepCompleted, result.Summary+"\n\nChild trace: "+result.OutputRef, "")
					if err != nil {
						return reconciled, err
					}
					eventType, terminal = domain.EventDelegationCompleted, true
					break
				}
			}
			if !terminal {
				return reconciled, fmt.Errorf("completed child run %s has no completed stage", child.ID)
			}
		case domain.RunFailedRecoverable:
			result = domain.DelegationResult{Status: domain.DelegationBlocked, BlockReason: domain.DelegationBlockReasonChildRecoveryRequired, Error: child.Error}
			_, err = appStore.UpdateCollaborationStep(item.ParentStageID, domain.CollaborationStepFailed, "", child.Error)
			if err != nil {
				return reconciled, err
			}
			eventType, terminal = domain.EventDelegationBlocked, true
		case domain.RunFailed:
			result = domain.DelegationResult{Status: domain.DelegationFailed, Error: child.Error}
			_, err = appStore.UpdateCollaborationStep(item.ParentStageID, domain.CollaborationStepFailed, "", child.Error)
			if err != nil {
				return reconciled, err
			}
			eventType, terminal = domain.EventDelegationFailed, true
		case domain.RunCanceled:
			result = domain.DelegationResult{Status: domain.DelegationCanceled, Error: child.Error}
			_, err = appStore.UpdateCollaborationStep(item.ParentStageID, domain.CollaborationStepFailed, "", child.Error)
			if err != nil {
				return reconciled, err
			}
			eventType, terminal = domain.EventDelegationCanceled, true
		}
		if !terminal {
			continue
		}
		updated, err := appStore.UpdateRunDelegation(item.ID, result)
		if err != nil {
			return reconciled, err
		}
		parent, ok, err := appStore.GetRun(item.ParentRunID)
		if err != nil || !ok {
			if err == nil {
				err = fmt.Errorf("delegation %s parent run %s not found", item.ID, item.ParentRunID)
			}
			return reconciled, err
		}
		_, err = appStore.CreateRunEvent(domain.RunEvent{
			Type: eventType, SchemaVersion: domain.CurrentRunEventSchemaVersion,
			RunID: parent.ID, ConversationID: parent.ConversationID,
			StageID: item.ParentStageID, TurnID: item.ParentTurnID,
			Payload: map[string]any{
				"delegation_id": updated.ID, "parent_run_id": updated.ParentRunID,
				"child_run_id": updated.ChildRunID, "status": updated.Status,
				"block_reason": updated.BlockReason,
				"summary":      updated.Summary, "output_ref": updated.OutputRef,
				"output_hash": updated.OutputHash, "output_bytes": updated.OutputBytes,
				"summary_truncated": updated.SummaryTruncated, "error": updated.Error,
				"synthetic": true, "reason": domain.InterruptedWorkerReason,
			},
		})
		if err != nil {
			return reconciled, err
		}
		reconciled++
	}
	return reconciled, nil
}
