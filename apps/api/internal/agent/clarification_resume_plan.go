package agent

import (
	"context"
	"encoding/json"

	"agentflow-platform/apps/api/internal/checkpoint"
	"agentflow-platform/apps/api/internal/clarification"
	"agentflow-platform/apps/api/internal/domain"
)

type clarificationResumeKind uint8

const (
	clarificationResumeCheckpoint clarificationResumeKind = iota
	clarificationResumeReplay
	clarificationResumeCollaboration
)

// A pre-claim decision: a committed Stage and replay output are evidence,
// not mutable execution cursors shared across the mode branches.
type clarificationResumePlan struct {
	kind           clarificationResumeKind
	run            domain.Run
	restored       restoredRuntime
	completedStage *domain.CollaborationStep
	output         string
}

func (r *Runtime) prepareClarificationResume(ctx context.Context, owner, workspace, runID, id string) (clarificationResumePlan, error) {
	if r.clarifications == nil {
		return clarificationResumePlan{}, clarification.ErrConflict
	}
	run, ok, err := r.store.GetRun(runID)
	if err != nil {
		return clarificationResumePlan{}, err
	}
	if !ok || run.WorkspaceID != workspace {
		return clarificationResumePlan{}, clarification.ErrConflict
	}
	restored, err := r.restoreRuntime(run)
	if err != nil {
		return clarificationResumePlan{}, err
	}
	// Validate frozen capabilities and uncertain effects before accepting execution.
	effects, err := r.store.ListToolEffects(runID)
	if err != nil {
		return clarificationResumePlan{}, err
	}
	for _, effect := range effects {
		if domain.ToolEffectRequiresReconciliation(effect.Status) {
			return clarificationResumePlan{}, checkpoint.ErrNeedsReconciliation
		}
	}
	records, err := r.clarifications.ListClarifications(ctx, owner, workspace, runID)
	if err != nil {
		return clarificationResumePlan{}, err
	}
	if len(records) == 0 || records[len(records)-1].ID != id {
		return clarificationResumePlan{}, clarification.ErrConflict
	}
	plan := clarificationResumePlan{run: run, restored: restored}
	return r.decideClarificationResume(plan, records[len(records)-1], effects)
}

func (r *Runtime) decideClarificationResume(plan clarificationResumePlan, previous domain.Clarification, effects []domain.ToolEffectRecord) (clarificationResumePlan, error) {
	if plan.run.Status != domain.RunFailedRecoverable || previous.Status != "applied" {
		return plan, nil
	}
	steps, err := r.store.ListCollaborationSteps(plan.run.ID)
	if err != nil {
		return clarificationResumePlan{}, err
	}
	routerCommitted := false
	for i := range steps {
		if steps[i].ID == previous.StageID && steps[i].Status == domain.CollaborationStepCompleted {
			plan.completedStage = &steps[i]
		}
		routerCommitted = routerCommitted || steps[i].Role == "router" && steps[i].Status == domain.CollaborationStepCompleted
	}
	// A completed Planner without a Router remains the plan-approval boundary.
	if plan.restored.mode == ChatModeMultiAgent && plan.completedStage != nil && routerCommitted {
		plan.kind = clarificationResumeCollaboration
		return plan, nil
	}
	if plan.completedStage == nil {
		for _, effect := range effects {
			if effect.CreatedAt.After(previous.CreatedAt) && effect.Status == domain.ToolEffectCommitted {
				return clarificationResumePlan{}, checkpoint.ErrNeedsReconciliation
			}
		}
	}
	if plan.restored.mode == ChatModeSingle {
		return r.findClarificationReplay(plan, previous.ID)
	}
	return plan, nil
}

func (r *Runtime) findClarificationReplay(plan clarificationResumePlan, id string) (clarificationResumePlan, error) {
	items, err := r.store.ListRunEvents(plan.run.ID)
	if err != nil {
		return clarificationResumePlan{}, err
	}
	resumedAt := int64(0)
	for _, item := range items {
		if item.Type == domain.EventRunResumed && item.Payload["kind"] == "clarification" {
			data, _ := json.Marshal(item.Payload["clarification"])
			var q domain.Clarification
			json.Unmarshal(data, &q)
			if q.ID == id {
				resumedAt = item.Sequence
			}
		}
		if resumedAt > 0 && item.Sequence > resumedAt && item.Type == domain.EventTurnCompleted && item.Payload["stop_reason"] == "completed" {
			plan.output, _ = item.Payload["output"].(string)
			plan.kind = clarificationResumeReplay
		}
	}
	return plan, nil
}

func restoreClarificationCheckpoint(data json.RawMessage, run domain.Run, record domain.Clarification) (clarificationCheckpoint, error) {
	var saved clarificationCheckpoint
	if err := json.Unmarshal(data, &saved); err != nil {
		return saved, err
	}
	hash, _, err := checkpoint.RuntimeHashes(run.RuntimeSnapshot)
	if err != nil || saved.Version != 1 || saved.SnapshotHash != hash || saved.Request.RunID != run.ID || saved.Request.ConversationID != run.ConversationID || saved.Request.StepID != record.StageID || saved.Request.TurnID != record.TurnID {
		return saved, checkpoint.ErrCheckpointStale
	}
	if !saved.HumanInput && saved.Continuation != nil && saved.Continuation.PendingCallID != record.ToolCallID {
		return saved, checkpoint.ErrCheckpointStale
	}
	return saved, nil
}
