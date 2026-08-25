package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	CurrentTaskStateSchemaVersion = 1
	MaxTaskStateBytes             = 16_000
)

type TaskItemStatus string

const (
	TaskItemPending    TaskItemStatus = "pending"
	TaskItemInProgress TaskItemStatus = "in_progress"
	TaskItemCompleted  TaskItemStatus = "completed"
	TaskItemCanceled   TaskItemStatus = "canceled"
)

type TaskBlockerStatus string

const (
	TaskBlockerOpen     TaskBlockerStatus = "open"
	TaskBlockerResolved TaskBlockerStatus = "resolved"
)

type TaskItem struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Details      string         `json:"details,omitempty"`
	Status       TaskItemStatus `json:"status"`
	ArtifactRefs []string       `json:"artifact_refs,omitempty"`
}

type TaskDecision struct {
	ID           string `json:"id"`
	Statement    string `json:"statement"`
	Rationale    string `json:"rationale,omitempty"`
	SupersedesID string `json:"supersedes_id,omitempty"`
}

type TaskConstraint struct {
	ID        string `json:"id"`
	Statement string `json:"statement"`
}

type TaskBlocker struct {
	ID          string            `json:"id"`
	Description string            `json:"description"`
	Status      TaskBlockerStatus `json:"status"`
}

// TaskState is the current structured state for one Conversation. It is a
// projection of TaskStateRevision snapshots, never an independently mutable row.
type TaskState struct {
	SchemaVersion  int              `json:"schema_version"`
	WorkspaceID    string           `json:"workspace_id"`
	ConversationID string           `json:"conversation_id"`
	Version        int64            `json:"version"`
	Goal           string           `json:"goal,omitempty"`
	Tasks          []TaskItem       `json:"tasks"`
	Decisions      []TaskDecision   `json:"decisions"`
	Constraints    []TaskConstraint `json:"constraints"`
	Blockers       []TaskBlocker    `json:"blockers"`
	ArtifactRefs   []string         `json:"artifact_refs"`
	UpdatedAt      time.Time        `json:"updated_at"`
}

type TaskStateOperationType string

const (
	TaskStateSetGoal           TaskStateOperationType = "set_goal"
	TaskStateClearGoal         TaskStateOperationType = "clear_goal"
	TaskStateUpsertTask        TaskStateOperationType = "upsert_task"
	TaskStateSetTaskStatus     TaskStateOperationType = "set_task_status"
	TaskStateRemoveTask        TaskStateOperationType = "remove_task"
	TaskStateAddDecision       TaskStateOperationType = "add_decision"
	TaskStateUpsertConstraint  TaskStateOperationType = "upsert_constraint"
	TaskStateRemoveConstraint  TaskStateOperationType = "remove_constraint"
	TaskStateUpsertBlocker     TaskStateOperationType = "upsert_blocker"
	TaskStateResolveBlocker    TaskStateOperationType = "resolve_blocker"
	TaskStateRemoveBlocker     TaskStateOperationType = "remove_blocker"
	TaskStateAddArtifactRef    TaskStateOperationType = "add_artifact_ref"
	TaskStateRemoveArtifactRef TaskStateOperationType = "remove_artifact_ref"
)

// TaskStateOperation is intentionally a closed command set. Callers can patch
// individual facts but cannot replace the whole state or its version metadata.
type TaskStateOperation struct {
	Type         TaskStateOperationType `json:"type"`
	Goal         string                 `json:"goal,omitempty"`
	Task         *TaskItem              `json:"task,omitempty"`
	TaskID       string                 `json:"task_id,omitempty"`
	TaskStatus   TaskItemStatus         `json:"task_status,omitempty"`
	Decision     *TaskDecision          `json:"decision,omitempty"`
	Constraint   *TaskConstraint        `json:"constraint,omitempty"`
	ConstraintID string                 `json:"constraint_id,omitempty"`
	Blocker      *TaskBlocker           `json:"blocker,omitempty"`
	BlockerID    string                 `json:"blocker_id,omitempty"`
	ArtifactRef  string                 `json:"artifact_ref,omitempty"`
}

type TaskStatePatch struct {
	ExpectedVersion int64                `json:"expected_version"`
	Operations      []TaskStateOperation `json:"operations"`
}

type TaskStateSource struct {
	ActorType       string `json:"actor_type"`
	ActorID         string `json:"actor_id,omitempty"`
	RunID           string `json:"run_id,omitempty"`
	StageID         string `json:"stage_id,omitempty"`
	TurnID          string `json:"turn_id,omitempty"`
	SourceMessageID string `json:"source_message_id,omitempty"`
}

// TaskStateRevision stores both the typed patch and its resulting immutable
// state snapshot, enabling a timeline and O(1) reconstruction of any version.
type TaskStateRevision struct {
	ID              string          `json:"id"`
	WorkspaceID     string          `json:"workspace_id"`
	ConversationID  string          `json:"conversation_id"`
	Version         int64           `json:"version"`
	PreviousVersion int64           `json:"previous_version"`
	Patch           TaskStatePatch  `json:"patch"`
	State           TaskState       `json:"state"`
	Source          TaskStateSource `json:"source"`
	CreatedAt       time.Time       `json:"created_at"`
}

func EmptyTaskState(workspaceID, conversationID string) TaskState {
	return TaskState{
		SchemaVersion: CurrentTaskStateSchemaVersion,
		WorkspaceID:   NormalizeWorkspaceID(workspaceID), ConversationID: strings.TrimSpace(conversationID),
		Tasks: []TaskItem{}, Decisions: []TaskDecision{}, Constraints: []TaskConstraint{},
		Blockers: []TaskBlocker{}, ArtifactRefs: []string{},
	}
}

func ApplyTaskStatePatch(current TaskState, patch TaskStatePatch, updatedAt time.Time) (TaskState, error) {
	if patch.ExpectedVersion < 0 {
		return TaskState{}, errors.New("task state expected_version cannot be negative")
	}
	if patch.ExpectedVersion != current.Version {
		return TaskState{}, fmt.Errorf("task state expected_version %d does not match current version %d", patch.ExpectedVersion, current.Version)
	}
	if len(patch.Operations) == 0 {
		return TaskState{}, errors.New("task state patch requires at least one operation")
	}
	if len(patch.Operations) > 50 {
		return TaskState{}, errors.New("task state patch exceeds 50 operations")
	}
	next := cloneTaskState(current)
	if next.SchemaVersion == 0 {
		next.SchemaVersion = CurrentTaskStateSchemaVersion
	}
	for index, operation := range patch.Operations {
		if err := applyTaskStateOperation(&next, operation); err != nil {
			return TaskState{}, fmt.Errorf("task state operation %d: %w", index+1, err)
		}
	}
	next.Version = current.Version + 1
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	next.UpdatedAt = updatedAt.UTC()
	encoded, err := json.Marshal(next)
	if err != nil {
		return TaskState{}, fmt.Errorf("encode task state: %w", err)
	}
	if len(encoded) > MaxTaskStateBytes {
		return TaskState{}, fmt.Errorf("task state exceeds %d bytes", MaxTaskStateBytes)
	}
	return next, nil
}

func applyTaskStateOperation(state *TaskState, operation TaskStateOperation) error {
	switch operation.Type {
	case TaskStateSetGoal:
		goal, err := normalizeTaskText("goal", operation.Goal, 2_000, true)
		if err != nil {
			return err
		}
		state.Goal = goal
	case TaskStateClearGoal:
		state.Goal = ""
	case TaskStateUpsertTask:
		if operation.Task == nil {
			return errors.New("upsert_task requires task")
		}
		item, err := normalizeTaskItem(*operation.Task)
		if err != nil {
			return err
		}
		state.Tasks = upsertTaskItem(state.Tasks, item)
	case TaskStateSetTaskStatus:
		id, err := normalizeTaskID("task_id", operation.TaskID)
		if err != nil {
			return err
		}
		if !validTaskItemStatus(operation.TaskStatus) {
			return errors.New("set_task_status requires a valid task_status")
		}
		found := false
		for index := range state.Tasks {
			if state.Tasks[index].ID == id {
				state.Tasks[index].Status, found = operation.TaskStatus, true
				break
			}
		}
		if !found {
			return fmt.Errorf("task %q not found", id)
		}
	case TaskStateRemoveTask:
		id, err := normalizeTaskID("task_id", operation.TaskID)
		if err != nil {
			return err
		}
		state.Tasks, err = removeTaskItem(state.Tasks, id)
		if err != nil {
			return err
		}
	case TaskStateAddDecision:
		if operation.Decision == nil {
			return errors.New("add_decision requires decision")
		}
		item, err := normalizeTaskDecision(*operation.Decision)
		if err != nil {
			return err
		}
		if containsDecision(state.Decisions, item.ID) {
			return fmt.Errorf("decision %q already exists", item.ID)
		}
		if item.SupersedesID != "" && !containsDecision(state.Decisions, item.SupersedesID) {
			return fmt.Errorf("superseded decision %q not found", item.SupersedesID)
		}
		state.Decisions = append(state.Decisions, item)
	case TaskStateUpsertConstraint:
		if operation.Constraint == nil {
			return errors.New("upsert_constraint requires constraint")
		}
		item, err := normalizeTaskConstraint(*operation.Constraint)
		if err != nil {
			return err
		}
		state.Constraints = upsertConstraint(state.Constraints, item)
	case TaskStateRemoveConstraint:
		id, err := normalizeTaskID("constraint_id", operation.ConstraintID)
		if err != nil {
			return err
		}
		state.Constraints, err = removeConstraint(state.Constraints, id)
		if err != nil {
			return err
		}
	case TaskStateUpsertBlocker:
		if operation.Blocker == nil {
			return errors.New("upsert_blocker requires blocker")
		}
		item, err := normalizeTaskBlocker(*operation.Blocker)
		if err != nil {
			return err
		}
		state.Blockers = upsertBlocker(state.Blockers, item)
	case TaskStateResolveBlocker:
		id, err := normalizeTaskID("blocker_id", operation.BlockerID)
		if err != nil {
			return err
		}
		found := false
		for index := range state.Blockers {
			if state.Blockers[index].ID == id {
				state.Blockers[index].Status, found = TaskBlockerResolved, true
				break
			}
		}
		if !found {
			return fmt.Errorf("blocker %q not found", id)
		}
	case TaskStateRemoveBlocker:
		id, err := normalizeTaskID("blocker_id", operation.BlockerID)
		if err != nil {
			return err
		}
		state.Blockers, err = removeBlocker(state.Blockers, id)
		if err != nil {
			return err
		}
	case TaskStateAddArtifactRef:
		value, err := normalizeTaskText("artifact_ref", operation.ArtifactRef, 500, true)
		if err != nil {
			return err
		}
		state.ArtifactRefs = appendUnique(state.ArtifactRefs, value)
	case TaskStateRemoveArtifactRef:
		value, err := normalizeTaskText("artifact_ref", operation.ArtifactRef, 500, true)
		if err != nil {
			return err
		}
		state.ArtifactRefs = removeString(state.ArtifactRefs, value)
	default:
		return fmt.Errorf("unsupported operation type %q", operation.Type)
	}
	return nil
}

func normalizeTaskItem(item TaskItem) (TaskItem, error) {
	var err error
	if item.ID, err = normalizeTaskID("task.id", item.ID); err != nil {
		return TaskItem{}, err
	}
	if item.Title, err = normalizeTaskText("task.title", item.Title, 500, true); err != nil {
		return TaskItem{}, err
	}
	if item.Details, err = normalizeTaskText("task.details", item.Details, 2_000, false); err != nil {
		return TaskItem{}, err
	}
	if item.Status == "" {
		item.Status = TaskItemPending
	}
	if !validTaskItemStatus(item.Status) {
		return TaskItem{}, fmt.Errorf("task status %q is invalid", item.Status)
	}
	item.ArtifactRefs, err = normalizeTaskStrings("task.artifact_refs", item.ArtifactRefs, 20, 500)
	return item, err
}

func normalizeTaskDecision(item TaskDecision) (TaskDecision, error) {
	var err error
	if item.ID, err = normalizeTaskID("decision.id", item.ID); err != nil {
		return TaskDecision{}, err
	}
	if item.Statement, err = normalizeTaskText("decision.statement", item.Statement, 1_000, true); err != nil {
		return TaskDecision{}, err
	}
	if item.Rationale, err = normalizeTaskText("decision.rationale", item.Rationale, 2_000, false); err != nil {
		return TaskDecision{}, err
	}
	if item.SupersedesID != "" {
		item.SupersedesID, err = normalizeTaskID("decision.supersedes_id", item.SupersedesID)
	}
	return item, err
}

func normalizeTaskConstraint(item TaskConstraint) (TaskConstraint, error) {
	var err error
	if item.ID, err = normalizeTaskID("constraint.id", item.ID); err != nil {
		return TaskConstraint{}, err
	}
	item.Statement, err = normalizeTaskText("constraint.statement", item.Statement, 1_000, true)
	return item, err
}

func normalizeTaskBlocker(item TaskBlocker) (TaskBlocker, error) {
	var err error
	if item.ID, err = normalizeTaskID("blocker.id", item.ID); err != nil {
		return TaskBlocker{}, err
	}
	if item.Description, err = normalizeTaskText("blocker.description", item.Description, 1_000, true); err != nil {
		return TaskBlocker{}, err
	}
	if item.Status == "" {
		item.Status = TaskBlockerOpen
	}
	if item.Status != TaskBlockerOpen && item.Status != TaskBlockerResolved {
		return TaskBlocker{}, fmt.Errorf("blocker status %q is invalid", item.Status)
	}
	return item, nil
}

func normalizeTaskID(field, value string) (string, error) {
	return normalizeTaskText(field, value, 128, true)
}

func normalizeTaskText(field, value string, maxRunes int, required bool) (string, error) {
	value = strings.TrimSpace(value)
	if required && value == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	if len([]rune(value)) > maxRunes {
		return "", fmt.Errorf("%s exceeds %d characters", field, maxRunes)
	}
	return value, nil
}

func normalizeTaskStrings(field string, values []string, maxItems, maxRunes int) ([]string, error) {
	if len(values) > maxItems {
		return nil, fmt.Errorf("%s exceeds %d items", field, maxItems)
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		normalized, err := normalizeTaskText(field, value, maxRunes, true)
		if err != nil {
			return nil, err
		}
		result = appendUnique(result, normalized)
	}
	return result, nil
}

func validTaskItemStatus(status TaskItemStatus) bool {
	switch status {
	case TaskItemPending, TaskItemInProgress, TaskItemCompleted, TaskItemCanceled:
		return true
	default:
		return false
	}
}

func upsertTaskItem(items []TaskItem, value TaskItem) []TaskItem {
	for index := range items {
		if items[index].ID == value.ID {
			items[index] = value
			return items
		}
	}
	return append(items, value)
}

func removeTaskItem(items []TaskItem, id string) ([]TaskItem, error) {
	for index := range items {
		if items[index].ID == id {
			return append(items[:index], items[index+1:]...), nil
		}
	}
	return items, fmt.Errorf("task %q not found", id)
}

func containsDecision(items []TaskDecision, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func upsertConstraint(items []TaskConstraint, value TaskConstraint) []TaskConstraint {
	for index := range items {
		if items[index].ID == value.ID {
			items[index] = value
			return items
		}
	}
	return append(items, value)
}

func removeConstraint(items []TaskConstraint, id string) ([]TaskConstraint, error) {
	for index := range items {
		if items[index].ID == id {
			return append(items[:index], items[index+1:]...), nil
		}
	}
	return items, fmt.Errorf("constraint %q not found", id)
}

func upsertBlocker(items []TaskBlocker, value TaskBlocker) []TaskBlocker {
	for index := range items {
		if items[index].ID == value.ID {
			items[index] = value
			return items
		}
	}
	return append(items, value)
}

func removeBlocker(items []TaskBlocker, id string) ([]TaskBlocker, error) {
	for index := range items {
		if items[index].ID == id {
			return append(items[:index], items[index+1:]...), nil
		}
	}
	return items, fmt.Errorf("blocker %q not found", id)
}

func appendUnique(items []string, value string) []string {
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}

func removeString(items []string, value string) []string {
	for index := range items {
		if items[index] == value {
			return append(items[:index], items[index+1:]...)
		}
	}
	return items
}

func cloneTaskState(state TaskState) TaskState {
	encoded, _ := json.Marshal(state)
	var cloned TaskState
	_ = json.Unmarshal(encoded, &cloned)
	if cloned.Tasks == nil {
		cloned.Tasks = []TaskItem{}
	}
	if cloned.Decisions == nil {
		cloned.Decisions = []TaskDecision{}
	}
	if cloned.Constraints == nil {
		cloned.Constraints = []TaskConstraint{}
	}
	if cloned.Blockers == nil {
		cloned.Blockers = []TaskBlocker{}
	}
	if cloned.ArtifactRefs == nil {
		cloned.ArtifactRefs = []string{}
	}
	return cloned
}
