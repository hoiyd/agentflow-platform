package toolloop_test

import (
	"context"
	"sync/atomic"
	"testing"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/inference/capture"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

func TestDiscoveryPostgresRestartRestoresVisibleSchema(t *testing.T) {
	url := pgfixture.DatabaseURL(t)
	storage, err := store.NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	workspaces := pgfixture.GrantMemberships(t, url, "discovery-fixture", "owner", "owned", "other")
	conversation, err := storage.CreateConversationInWorkspace(workspaces[0], "discovery restart")
	if err != nil {
		t.Fatal(err)
	}
	limits := domain.RuntimeRunBudget{MaxModelCalls: 6, MaxToolCalls: 4}
	config := domain.ToolSchemaConfig{Mode: "lazy"}.Normalize()
	var executed atomic.Int32
	f := newLoopFixture(t, limits, func(round int, input wireRequest) any {
		switch round {
		case 1:
			return call("tool_search", `{"query":"calculator"}`)
		case 2:
			return modelResponse(provider.ChatChoice{Content: "partial"}, "length")
		case 3:
			if !hasWireTool(input, "calculator") {
				t.Error("restart lost activated Schema")
			}
			return call("calculator", `{"expression":"1 + 1"}`)
		default:
			return answer("2")
		}
	})
	f.request.Catalog = discoveryCatalog(t, &executed)
	f.run, err = storage.CreateRunWithContract("agent_planner", conversation.ID, domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &limits, ToolSchema: &config,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	attach := func(turnID string) {
		f.ctx = eventpkg.WithScope(context.Background(), eventpkg.Scope{RunID: f.run.ID, ConversationID: conversation.ID, StageID: "stage-fixed", TurnID: turnID})
		f.ctx = budget.WithController(f.ctx, budget.NewTracker(storage, eventpkg.StoreSink{Store: storage}, f.run))
		f.ctx = contextassembly.WithSession(f.ctx, contextassembly.Session{Config: contextassembly.DefaultConfig(), Sink: eventpkg.StoreSink{Store: storage}})
		f.request.Trace = provider.ChatTrace{RunID: f.run.ID, StepID: "stage-fixed", Recorder: eventpkg.NewRecorder(storage)}
		f.request.AgentID, f.request.SchemaConfig = "agent_planner", config
		f.request.Sink = eventpkg.StoreSink{Store: storage}
		f.request.RunEvents = func() ([]domain.RunEvent, error) { return storage.ListRunEvents(f.run.ID) }
		f.request.ExecutorOptions.ArtifactStore, f.request.ExecutorOptions.EffectJournal = storage, storage
		f.client.SetRequestRecorder(capture.NewRecorder(storage, capture.Options{Mode: domain.ModelRequestCaptureFull}))
	}
	attach("before-restart")
	if _, err := f.execute(); failure.Describe(err).Code != "incomplete_output" {
		t.Fatal(err)
	}
	if err := storage.Close(); err != nil {
		t.Fatal(err)
	}
	storage, err = store.NewPostgresStore(url)
	if err != nil {
		t.Fatal(err)
	}
	replay, ok, err := storage.GetRunReplay(f.run.ID)
	if err != nil || !ok || replay.RuntimeSnapshot == nil || replay.RuntimeSnapshot.ToolSchema == nil || *replay.RuntimeSnapshot.ToolSchema != config {
		t.Fatalf("frozen config round-trip failed: replay=%#v err=%v", replay.RuntimeSnapshot, err)
	}
	other := storage.ForWorkspace(domain.NewWorkspaceScope(workspaces[1]))
	if _, found, err := other.GetRunReplay(f.run.ID); err != nil || found {
		t.Fatalf("cross-Workspace replay found=%t err=%v", found, err)
	}
	attach("after-restart")
	if output, err := f.execute(); err != nil || output != "2" || executed.Load() != 1 {
		t.Fatalf("output=%q executions=%d err=%v", output, executed.Load(), err)
	}
	items, err := storage.ListRunEvents(f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	searches := 0
	for _, item := range items {
		if item.Type == domain.EventToolDiscoveryUpdated && item.Payload["reason"] == "search_admitted" {
			searches++
		}
	}
	if searches != 1 {
		t.Fatalf("restart rediscovered instead of restoring: searches=%d", searches)
	}
}
