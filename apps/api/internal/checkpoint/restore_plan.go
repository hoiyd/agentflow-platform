package checkpoint

import (
	"context"
	"errors"
	"fmt"

	"agentflow-platform/apps/api/internal/domain"
)

type stageRestore struct {
	checkpoint domain.StageCheckpoint
	terminal   *domain.RunEvent
}

// ValidateResume makes the same decisions as RestoreRun without recording facts
// or changing checkpoints. Runtime accepts execution before applying the plan.
func (p *InternalProvider) ValidateResume(_ context.Context, run domain.Run) error {
	_, err := p.planRestore(run)
	return err
}

func (p *InternalProvider) planRestore(run domain.Run) ([]stageRestore, error) {
	if p == nil || p.store == nil {
		return nil, errors.New("checkpoint provider is unavailable")
	}
	snapshotHash, toolHash, err := RuntimeHashes(run.RuntimeSnapshot)
	if err != nil {
		return nil, err
	}
	checkpoints, err := p.store.ListStageCheckpoints(run.ID)
	if err != nil {
		return nil, err
	}
	effects, err := p.store.ListToolEffects(run.ID)
	if err != nil {
		return nil, err
	}
	events, err := p.store.ListRunEvents(run.ID)
	if err != nil {
		return nil, err
	}
	byStage := make(map[string][]domain.ToolEffectRecord)
	for _, effect := range effects {
		byStage[effect.StageID] = append(byStage[effect.StageID], effect)
	}
	plan := make([]stageRestore, 0, len(checkpoints))
	for _, item := range checkpoints {
		if item.Provider != InternalStateProvider || item.RuntimeSnapshotHash != snapshotHash || item.ToolDefinitionsHash != toolHash {
			return nil, fmt.Errorf("%w: stage %s", ErrCheckpointStale, item.StageID)
		}
		action, err := planStageRestore(item, events, byStage[item.StageID])
		if err != nil {
			return nil, err
		}
		plan = append(plan, action)
	}
	return plan, nil
}

func planStageRestore(item domain.StageCheckpoint, events []domain.RunEvent, effects []domain.ToolEffectRecord) (stageRestore, error) {
	action := stageRestore{checkpoint: item}
	status := item.Status
	if status == domain.CheckpointPrepared || status == domain.CheckpointExecuting {
		if terminal, ok := stageTerminalAfter(events, item.StageID, item.EventCursor); ok {
			action.terminal = &terminal
			status = domain.CheckpointNeedsReconciliation
			if terminal.Type == domain.EventStageCompleted {
				status = domain.CheckpointCommitted
			}
			if status == domain.CheckpointCommitted && hasUncertainEffect(effects) {
				return action, fmt.Errorf("%w: stage %s completed with an uncertain tool effect", ErrNeedsReconciliation, item.StageID)
			}
		}
	}
	if status != domain.CheckpointCommitted {
		for _, effect := range effects {
			if effect.Status == domain.ToolEffectCommitted {
				return action, fmt.Errorf("%w: unfinished stage %s has a committed external effect; automatic stage recreation is unsafe", ErrNeedsReconciliation, item.StageID)
			}
		}
	}
	switch status {
	case domain.CheckpointCommitted, domain.CheckpointCompensated:
	case domain.CheckpointPrepared, domain.CheckpointExecuting, domain.CheckpointNeedsReconciliation:
		if hasUncertainEffect(effects) {
			return action, fmt.Errorf("%w: stage %s has an uncertain tool effect", ErrNeedsReconciliation, item.StageID)
		}
	default:
		return action, fmt.Errorf("unknown checkpoint status %q", status)
	}
	return action, nil
}
