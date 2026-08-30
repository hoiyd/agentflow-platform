package taskstate

import (
	"context"
	"encoding/json"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tools"
)

func TestUpdateTaskStateToolAppliesPatchAndPublishesEvent(t *testing.T) {
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatal(err)
	}
	conversation, _ := fileStore.CreateConversation("tool test")
	run, err := fileStore.CreateRunWithContract("agent_planner", conversation.ID, taskStateTestSnapshot(), nil)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(fileStore, eventpkg.StoreSink{Store: fileStore})
	catalog, err := tools.NewCatalog(service.ToolBinding())
	if err != nil {
		t.Fatal(err)
	}
	ctx := eventpkg.WithScope(context.Background(), eventpkg.Scope{
		ConversationID: conversation.ID, RunID: run.ID, StageID: "stage-1", TurnID: "turn-1",
	})
	arguments := json.RawMessage(`{"expected_version":0,"operations":[{"type":"set_goal","goal":"Keep exact task facts durable"}]}`)
	executor := tools.NewExecutor(catalog, tools.ExecutorOptions{EffectJournal: fileStore})
	request := tools.ExecutionRequest{
		CallID: "call-1", RunID: run.ID, StageID: "stage-1", TurnID: "turn-1",
		Tool: UpdateToolName, Arguments: arguments,
	}
	result := executor.Execute(ctx, request)
	if result.Error != nil {
		t.Fatalf("execute task state tool: %#v", result.Error)
	}
	state, ok, err := fileStore.GetTaskState(conversation.ID)
	if err != nil || !ok || state.Version != 1 || state.Goal != "Keep exact task facts durable" {
		t.Fatalf("persisted state: state=%#v ok=%v err=%v", state, ok, err)
	}
	revisions, err := fileStore.ListTaskStateRevisions(conversation.ID)
	if err != nil || len(revisions) != 1 || revisions[0].Source.ActorType != "model" || revisions[0].Source.ActorID != "agent_planner" || revisions[0].Source.TurnID != "turn-1" {
		t.Fatalf("revision provenance: revisions=%#v err=%v", revisions, err)
	}
	events, err := fileStore.ListRunEvents(run.ID)
	if err != nil || len(events) != 1 || events[0].Type != domain.EventTaskStateUpdated || events[0].Payload["version"] != float64(1) {
		t.Fatalf("task state event: events=%#v err=%v", events, err)
	}
	replayed := executor.Execute(ctx, request)
	if replayed.Error != nil || !replayed.Replayed {
		t.Fatalf("expected committed task state call to replay: %#v", replayed)
	}
	if revisions, err = fileStore.ListTaskStateRevisions(conversation.ID); err != nil || len(revisions) != 1 {
		t.Fatalf("replayed call appended another revision: revisions=%#v err=%v", revisions, err)
	}

	stale := executor.Execute(ctx, tools.ExecutionRequest{
		CallID: "call-2", RunID: run.ID, StageID: "stage-1", TurnID: "turn-1",
		Tool: UpdateToolName, Arguments: arguments,
	})
	if stale.Error == nil || stale.Error.Message == "" {
		t.Fatalf("expected stale tool patch to return an error: %#v", stale)
	}
}

func taskStateTestSnapshot() domain.RuntimeSnapshot {
	return domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, Mode: "single",
		Agent:           domain.RuntimeAgentSnapshot{ID: "agent_planner", Executor: domain.DefaultAgentExecutor},
		Model:           domain.RuntimeModelSnapshot{Provider: "local", Model: "test"},
		ContextAssembly: domain.ContextAssemblyConfig{AssemblerVersion: "context-assembler-v1", ContextWindowTokens: 128000},
		RunBudget:       &domain.RuntimeRunBudget{},
	}
}
