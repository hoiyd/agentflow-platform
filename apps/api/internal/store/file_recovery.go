package store

import (
	"errors"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func (s *FileStore) RepairInterruptedRun(request domain.InterruptedRunRepair) (domain.InterruptedRunRepairResult, error) {
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
		return domain.InterruptedRunRepairResult{}, ErrNotFound("run")
	}
	run := &s.data.Runs[runIndex]
	if run.Status != domain.RunRunning || (run.HeartbeatAt != nil && !run.HeartbeatAt.Before(request.StaleBefore)) {
		return domain.InterruptedRunRepairResult{Run: cloneRun(*run)}, nil
	}

	var cursor int64
	for _, item := range s.data.RunEvents {
		if item.RunID == request.RunID && item.Sequence > cursor {
			cursor = item.Sequence
		}
	}
	if cursor != request.ExpectedEventCursor {
		return domain.InterruptedRunRepairResult{}, errors.New("run event cursor changed during recovery")
	}

	now := time.Now().UTC()
	originalRun := cloneRun(*run)
	originalEventCount := len(s.data.RunEvents)
	originalSteps := append([]domain.CollaborationStep(nil), s.data.CollaborationSteps...)
	appended := make([]domain.RunEvent, 0, len(request.TerminalEvents))
	for _, item := range request.TerminalEvents {
		cursor++
		item.RunID = request.RunID
		prepared, err := prepareRunEvent(item, cursor, now)
		if err != nil {
			return domain.InterruptedRunRepairResult{}, err
		}
		s.data.RunEvents = append(s.data.RunEvents, prepared)
		appended = append(appended, prepared)
	}
	for index := range s.data.CollaborationSteps {
		step := &s.data.CollaborationSteps[index]
		if step.RunID == request.RunID && step.Status == domain.CollaborationStepRunning {
			step.Status = domain.CollaborationStepFailed
			step.Error = request.ErrorMessage
			step.UpdatedAt = now
		}
	}
	applyRunStatus(run, domain.RunFailedRecoverable, request.ErrorMessage, now)
	if err := s.saveLocked(); err != nil {
		s.data.Runs[runIndex] = originalRun
		s.data.RunEvents = s.data.RunEvents[:originalEventCount]
		s.data.CollaborationSteps = originalSteps
		return domain.InterruptedRunRepairResult{}, err
	}
	return domain.InterruptedRunRepairResult{Run: cloneRun(*run), AppendedEvents: appended, Applied: true}, nil
}
