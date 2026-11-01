package fixturestore

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
	"time"
)

func (s *Store) RepairInterruptedRun(request domain.InterruptedRunRepair) (domain.InterruptedRunRepairResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	runIndex := -1
	for index := range s.data.Runs {
		if s.data.Runs[index].ID == request.RunID {
			runIndex = index
			break
		}
	}
	if runIndex < 0 {
		return domain.InterruptedRunRepairResult{}, store.ErrNotFound("run")
	}
	run := &s.data.Runs[runIndex]
	if run.Status != request.ExpectedStatus || (run.Status != domain.RunRunning && run.Status != domain.RunCanceling) || (run.HeartbeatAt != nil && !run.HeartbeatAt.Before(request.StaleBefore)) {
		return domain.InterruptedRunRepairResult{Run: store.CloneRun(*run)}, nil
	}

	var cursor int64
	for _, item := range s.data.RunEvents {
		if item.RunID == request.RunID && item.Sequence > cursor {
			cursor = item.Sequence
		}
	}
	if cursor != request.ExpectedEventCursor {
		return domain.InterruptedRunRepairResult{Run: store.CloneRun(*run)}, nil
	}

	now := time.Now().UTC()
	appended := make([]domain.RunEvent, 0, len(request.TerminalEvents))
	for _, item := range request.TerminalEvents {
		cursor++
		item.RunID = request.RunID
		prepared, err := store.PrepareRunEvent(item, cursor, now)
		if err != nil {
			return domain.InterruptedRunRepairResult{}, err
		}
		appended = append(appended, prepared)
	}
	s.data.RunEvents = append(s.data.RunEvents, appended...)
	for index := range s.data.CollaborationSteps {
		step := &s.data.CollaborationSteps[index]
		if step.RunID == request.RunID && step.Status == domain.CollaborationStepRunning {
			step.Status = domain.CollaborationStepFailed
			step.Error = request.ErrorMessage
			step.UpdatedAt = now
		}
	}
	for index := range s.data.ToolEffects {
		effect := &s.data.ToolEffects[index]
		if effect.RunID == request.RunID && effect.Status == domain.ToolEffectExecuting {
			effect.Status = domain.ToolEffectNeedsReconciliation
			effect.Version = max(effect.Version, 1) + 1
			effect.Error = request.ErrorMessage
			effect.UpdatedAt = now
		}
	}
	status := domain.RunFailedRecoverable
	if run.Status == domain.RunCanceling {
		status = domain.RunCanceled
	}
	store.ApplyRunStatus(run, status, request.ErrorMessage, now)

	return domain.InterruptedRunRepairResult{Run: store.CloneRun(*run), AppendedEvents: appended, Applied: true}, nil
}
