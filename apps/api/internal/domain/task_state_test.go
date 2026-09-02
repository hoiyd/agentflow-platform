package domain

import (
	"strings"
	"testing"
	"time"
)

func TestApplyTaskStatePatchBuildsVersionedStructuredState(t *testing.T) {
	current := EmptyTaskState("workspace-a", "conversation-1")
	first, err := ApplyTaskStatePatch(current, TaskStatePatch{ExpectedVersion: 0, Operations: []TaskStateOperation{
		{Type: TaskStateSetGoal, Goal: "Ship H-07 without relying on a summary"},
		{Type: TaskStateUpsertTask, Task: &TaskItem{ID: "persist", Title: "Persist revisions", Status: TaskItemInProgress, ArtifactRefs: []string{"schema"}}},
		{Type: TaskStateAddDecision, Decision: &TaskDecision{ID: "scope", Statement: "Task state belongs to the conversation"}},
		{Type: TaskStateUpsertConstraint, Constraint: &TaskConstraint{ID: "compat", Statement: "Preserve existing behavior"}},
		{Type: TaskStateUpsertBlocker, Blocker: &TaskBlocker{ID: "tests", Description: "Postgres tests pending", Status: TaskBlockerOpen}},
		{Type: TaskStateAddArtifactRef, ArtifactRef: "docs/runtime/task-state.md"},
	}}, time.Unix(100, 0))
	if err != nil {
		t.Fatalf("apply first patch: %v", err)
	}
	if first.Version != 1 || first.Goal == "" || len(first.Tasks) != 1 || len(first.Decisions) != 1 || len(first.Constraints) != 1 || len(first.Blockers) != 1 || len(first.ArtifactRefs) != 1 {
		t.Fatalf("unexpected first state: %#v", first)
	}
	if current.Version != 0 || len(current.Tasks) != 0 {
		t.Fatalf("patch mutated prior version: %#v", current)
	}

	second, err := ApplyTaskStatePatch(first, TaskStatePatch{ExpectedVersion: 1, Operations: []TaskStateOperation{
		{Type: TaskStateSetTaskStatus, TaskID: "persist", TaskStatus: TaskItemCompleted},
		{Type: TaskStateResolveBlocker, BlockerID: "tests"},
		{Type: TaskStateRemoveConstraint, ConstraintID: "compat"},
		{Type: TaskStateAddDecision, Decision: &TaskDecision{ID: "scope-v2", Statement: "Keep immutable snapshots", SupersedesID: "scope"}},
	}}, time.Unix(200, 0))
	if err != nil {
		t.Fatalf("apply second patch: %v", err)
	}
	if second.Version != 2 || second.Tasks[0].Status != TaskItemCompleted || second.Blockers[0].Status != TaskBlockerResolved || len(second.Constraints) != 0 || len(second.Decisions) != 2 {
		t.Fatalf("unexpected second state: %#v", second)
	}
	if first.Tasks[0].Status != TaskItemInProgress || first.Blockers[0].Status != TaskBlockerOpen {
		t.Fatalf("second patch mutated first snapshot: %#v", first)
	}
}

func TestApplyTaskStatePatchRejectsInvalidOrOversizedChanges(t *testing.T) {
	current := EmptyTaskState("workspace", "conversation")
	tests := []struct {
		name  string
		patch TaskStatePatch
		want  string
	}{
		{name: "empty", patch: TaskStatePatch{}, want: "requires at least one operation"},
		{name: "unknown operation", patch: TaskStatePatch{Operations: []TaskStateOperation{{Type: "replace_all"}}}, want: "unsupported operation"},
		{name: "missing task", patch: TaskStatePatch{Operations: []TaskStateOperation{{Type: TaskStateSetTaskStatus, TaskID: "missing", TaskStatus: TaskItemCompleted}}}, want: "not found"},
		{name: "oversized goal", patch: TaskStatePatch{Operations: []TaskStateOperation{{Type: TaskStateSetGoal, Goal: strings.Repeat("x", 2001)}}}, want: "exceeds 2000 characters"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ApplyTaskStatePatch(current, test.patch, time.Time{}); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
		})
	}
}
