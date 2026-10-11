package store

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
	"testing"
)

func TestPostgresStructuredTaskStateRoundTrip(t *testing.T) {
	databaseURL := pgfixture.DatabaseURL(t)
	postgresStore, err := NewPostgresStore(databaseURL)
	if err != nil {
		t.Fatalf("new postgres store: %v", err)
	}
	defer postgresStore.Close()
	conversation, err := postgresStore.CreateConversation("Postgres task state")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = postgresStore.DeleteConversation(conversation.ID) })
	run, err := postgresStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	first, err := postgresStore.ApplyTaskStatePatch(conversation.ID, domain.TaskStatePatch{ExpectedVersion: 0, Operations: []domain.TaskStateOperation{
		{Type: domain.TaskStateSetGoal, Goal: "Persist exact task facts"},
		{Type: domain.TaskStateUpsertConstraint, Constraint: &domain.TaskConstraint{ID: "scope", Statement: "Stay workspace scoped"}},
	}}, domain.TaskStateSource{ActorType: "model", RunID: run.ID})
	if err != nil || first.Version != 1 || first.State.WorkspaceID != conversation.WorkspaceID {
		t.Fatalf("first postgres revision: revision=%#v err=%v", first, err)
	}
	if _, err := postgresStore.ApplyTaskStatePatch(conversation.ID, domain.TaskStatePatch{ExpectedVersion: 0, Operations: []domain.TaskStateOperation{{Type: domain.TaskStateClearGoal}}}, domain.TaskStateSource{ActorType: "user"}); !IsTaskStateVersionConflict(err) {
		t.Fatalf("expected postgres version conflict, got %v", err)
	}
	second, err := postgresStore.ApplyTaskStatePatch(conversation.ID, domain.TaskStatePatch{ExpectedVersion: 1, Operations: []domain.TaskStateOperation{
		{Type: domain.TaskStateUpsertTask, Task: &domain.TaskItem{ID: "tests", Title: "Verify Postgres", Status: domain.TaskItemCompleted}},
	}}, domain.TaskStateSource{ActorType: "user"})
	if err != nil || second.Version != 2 {
		t.Fatalf("second postgres revision: revision=%#v err=%v", second, err)
	}
	state, ok, err := postgresStore.GetTaskState(conversation.ID)
	if err != nil || !ok || state.Version != 2 || len(state.Tasks) != 1 {
		t.Fatalf("postgres current task state: state=%#v ok=%v err=%v", state, ok, err)
	}
	historical, ok, err := postgresStore.GetTaskStateRevision(conversation.ID, 1)
	if err != nil || !ok || historical.State.Goal == "" {
		t.Fatalf("postgres historical task state: revision=%#v ok=%v err=%v", historical, ok, err)
	}
	replay, ok, err := postgresStore.GetRunReplay(run.ID)
	if err != nil || !ok || len(replay.TaskStateRevisions) != 2 {
		t.Fatalf("postgres task state replay: revisions=%#v ok=%v err=%v", replay.TaskStateRevisions, ok, err)
	}
}
