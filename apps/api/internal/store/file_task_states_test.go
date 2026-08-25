package store

import (
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestFileStoreTaskStateRevisionRoundTripConflictAndReplay(t *testing.T) {
	path := t.TempDir() + "/agentflow.json"
	first, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := first.CreateConversationInWorkspace("workspace-a", "task state")
	if err != nil {
		t.Fatal(err)
	}
	run, err := first.CreateRun("agent_planner", conversation.ID, testRuntimeSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	firstRevision, err := first.ApplyTaskStatePatch(conversation.ID, domain.TaskStatePatch{ExpectedVersion: 0, Operations: []domain.TaskStateOperation{
		{Type: domain.TaskStateSetGoal, Goal: "Implement durable task state"},
	}}, domain.TaskStateSource{ActorType: "model", RunID: run.ID, StageID: "stage-1"})
	if err != nil {
		t.Fatalf("apply first revision: %v", err)
	}
	if firstRevision.Version != 1 || firstRevision.State.WorkspaceID != "workspace-a" || firstRevision.Source.RunID != run.ID {
		t.Fatalf("unexpected first revision: %#v", firstRevision)
	}
	firstRevision.Patch.Operations[0].Goal = "mutated by caller"
	immutable, ok, err := first.GetTaskStateRevision(conversation.ID, 1)
	if err != nil || !ok || immutable.Patch.Operations[0].Goal != "Implement durable task state" {
		t.Fatalf("stored revision was mutated by caller: revision=%#v ok=%v err=%v", immutable, ok, err)
	}
	if _, err := first.ApplyTaskStatePatch(conversation.ID, domain.TaskStatePatch{ExpectedVersion: 0, Operations: []domain.TaskStateOperation{{Type: domain.TaskStateClearGoal}}}, domain.TaskStateSource{ActorType: "user"}); err == nil || !IsTaskStateVersionConflict(err) {
		t.Fatalf("expected stale version conflict, got %v", err)
	}
	secondRevision, err := first.ApplyTaskStatePatch(conversation.ID, domain.TaskStatePatch{ExpectedVersion: 1, Operations: []domain.TaskStateOperation{
		{Type: domain.TaskStateUpsertTask, Task: &domain.TaskItem{ID: "tests", Title: "Add round-trip tests", Status: domain.TaskItemCompleted}},
	}}, domain.TaskStateSource{ActorType: "user"})
	if err != nil {
		t.Fatalf("apply second revision: %v", err)
	}
	if secondRevision.Version != 2 || secondRevision.PreviousVersion != 1 {
		t.Fatalf("unexpected second revision: %#v", secondRevision)
	}

	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	state, ok, err := reopened.GetTaskState(conversation.ID)
	if err != nil || !ok || state.Version != 2 || len(state.Tasks) != 1 {
		t.Fatalf("current state round trip: state=%#v ok=%v err=%v", state, ok, err)
	}
	revision, ok, err := reopened.GetTaskStateRevision(conversation.ID, 1)
	if err != nil || !ok || revision.State.Goal == "" || revision.State.Version != 1 {
		t.Fatalf("historical version round trip: revision=%#v ok=%v err=%v", revision, ok, err)
	}
	revisions, err := reopened.ListTaskStateRevisions(conversation.ID)
	if err != nil || len(revisions) != 2 || revisions[0].Version != 1 || revisions[1].Version != 2 {
		t.Fatalf("revision timeline: revisions=%#v err=%v", revisions, err)
	}
	replay, ok, err := reopened.GetRunReplay(run.ID)
	if err != nil || !ok || len(replay.TaskStateRevisions) != 2 {
		t.Fatalf("task state replay: replay=%#v ok=%v err=%v", replay.TaskStateRevisions, ok, err)
	}
}

func TestFileStoreTaskStateRejectsCrossConversationSource(t *testing.T) {
	fileStore, err := NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatal(err)
	}
	first, _ := fileStore.CreateConversation("first")
	second, _ := fileStore.CreateConversation("second")
	run, _ := fileStore.CreateRun("agent_planner", second.ID, testRuntimeSnapshot())
	_, err = fileStore.ApplyTaskStatePatch(first.ID, domain.TaskStatePatch{ExpectedVersion: 0, Operations: []domain.TaskStateOperation{{Type: domain.TaskStateSetGoal, Goal: "invalid"}}}, domain.TaskStateSource{ActorType: "model", RunID: run.ID})
	if err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("expected cross-conversation source rejection, got %v", err)
	}
}
