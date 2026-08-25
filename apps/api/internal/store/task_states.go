package store

import (
	"encoding/json"
	"errors"
	"fmt"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
)

type TaskStateVersionConflict struct {
	Expected int64
	Actual   int64
}

type taskStateValidationFailure struct{ err error }

func (e *taskStateValidationFailure) Error() string { return e.err.Error() }
func (e *taskStateValidationFailure) Unwrap() error { return e.err }

func (e *taskStateValidationFailure) FailureInfo() failure.Info {
	return failure.Info{Code: "task_state_invalid_patch", Source: "task_state", Category: failure.CategoryValidation}
}

func (e *TaskStateVersionConflict) Error() string {
	return fmt.Sprintf("task state version conflict: expected=%d actual=%d", e.Expected, e.Actual)
}

func (e *TaskStateVersionConflict) FailureInfo() failure.Info {
	return failure.Info{
		Code: "task_state_version_conflict", Source: "task_state", Category: failure.CategoryValidation,
		Details: map[string]any{"expected_version": e.Expected, "actual_version": e.Actual},
	}
}

func IsTaskStateVersionConflict(err error) bool {
	var conflict *TaskStateVersionConflict
	return errors.As(err, &conflict)
}

func classifyTaskStateValidation(err error) error {
	var validation *taskStateValidationFailure
	if err == nil || errors.As(err, &validation) {
		return err
	}
	return &taskStateValidationFailure{err: err}
}

func cloneTaskStateRevision(revision domain.TaskStateRevision) domain.TaskStateRevision {
	encoded, _ := json.Marshal(revision)
	var cloned domain.TaskStateRevision
	_ = json.Unmarshal(encoded, &cloned)
	return cloned
}

func cloneTaskState(state domain.TaskState) domain.TaskState {
	encoded, _ := json.Marshal(state)
	var cloned domain.TaskState
	_ = json.Unmarshal(encoded, &cloned)
	return cloned
}
