package toolartifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tools"
)

func TestArtifactToolsReadSearchAndTraceWithinRun(t *testing.T) {
	fileStore, run, artifact := artifactFixture(t, time.Now().UTC().Add(time.Hour))
	service := NewService(fileStore, eventpkg.NewRecorder(fileStore))
	catalog, err := tools.NewCatalog(service.ToolBindings()...)
	if err != nil {
		t.Fatal(err)
	}
	ctx := eventpkg.WithScope(context.Background(), eventpkg.Scope{
		RunID: run.ID, ConversationID: run.ConversationID, StageID: "stage-1", TurnID: "turn-1",
	})
	executor := tools.NewExecutor(catalog, tools.ExecutorOptions{})
	read := executor.Execute(ctx, tools.ExecutionRequest{
		CallID: "read-1", RunID: run.ID, Tool: ReadToolName,
		Arguments: json.RawMessage(`{"artifact_id":"` + artifact.ID + `","limit":16}`),
	})
	if read.Error != nil || read.Result.(domain.ToolArtifactRead).NextOffset != 16 {
		t.Fatalf("artifact read failed: %#v", read)
	}
	search := executor.Execute(ctx, tools.ExecutionRequest{
		CallID: "search-1", RunID: run.ID, Tool: SearchToolName,
		Arguments: json.RawMessage(`{"artifact_id":"` + artifact.ID + `","query":"needle"}`),
	})
	if search.Error != nil || len(search.Result.(domain.ToolArtifactSearchResult).Matches) != 1 {
		t.Fatalf("artifact search failed: %#v", search)
	}
	events, err := fileStore.ListRunEvents(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	readEvents := 0
	for _, item := range events {
		if item.Type == domain.EventArtifactRead {
			readEvents++
		}
	}
	if readEvents != 2 {
		t.Fatalf("artifact access events = %d, want 2: %#v", readEvents, events)
	}
}

func TestArtifactToolRecordsExpiredAccess(t *testing.T) {
	fileStore, run, artifact := artifactFixture(t, time.Now().UTC().Add(-time.Minute))
	service := NewService(fileStore, eventpkg.NewRecorder(fileStore))
	catalog, _ := tools.NewCatalog(service.ToolBindings()...)
	ctx := eventpkg.WithScope(context.Background(), eventpkg.Scope{RunID: run.ID, ConversationID: run.ConversationID})
	result := tools.NewExecutor(catalog, tools.ExecutorOptions{}).Execute(ctx, tools.ExecutionRequest{
		CallID: "read-expired", RunID: run.ID, Tool: ReadToolName,
		Arguments: json.RawMessage(`{"artifact_id":"` + artifact.ID + `"}`),
	})
	if result.Error == nil || !errors.Is(result.Error.Cause, store.ErrToolArtifactExpired) {
		t.Fatalf("expired read result = %#v", result)
	}
	events, _ := fileStore.ListRunEvents(run.ID)
	found := false
	for _, item := range events {
		found = found || item.Type == domain.EventArtifactExpired
	}
	if !found {
		t.Fatalf("expired access event missing: %#v", events)
	}
}

func TestArtifactServiceOptionalAndFailurePaths(t *testing.T) {
	if service := NewService(nil, nil); service != nil {
		t.Fatalf("nil store created service: %#v", service)
	}
	var nilService *Service
	if nilService.ToolBindings() != nil || nilService.ToolNames() != nil {
		t.Fatal("nil service exposed artifact tools")
	}

	fileStore, run, artifact := artifactFixture(t, time.Now().UTC().Add(time.Hour))
	service := NewService(fileStore, nil)
	if names := service.ToolNames(); len(names) != 2 || names[0] != ReadToolName || names[1] != SearchToolName {
		t.Fatalf("artifact tool names = %#v", names)
	}
	readBinding := service.readBinding()
	searchBinding := service.searchBinding()
	if _, err := readBinding.Handler(context.Background(), json.RawMessage(`{`)); err == nil {
		t.Fatal("malformed read arguments were accepted")
	}
	if _, err := searchBinding.Handler(context.Background(), json.RawMessage(`{`)); err == nil {
		t.Fatal("malformed search arguments were accepted")
	}
	if _, err := readBinding.Handler(context.Background(), json.RawMessage(`{"artifact_id":"`+artifact.ID+`"}`)); err == nil {
		t.Fatal("artifact read without run scope was accepted")
	}
	if _, err := searchBinding.Handler(context.Background(), json.RawMessage(`{"artifact_id":"`+artifact.ID+`","query":"needle"}`)); err == nil {
		t.Fatal("artifact search without run scope was accepted")
	}

	ctx := eventpkg.WithScope(context.Background(), eventpkg.Scope{RunID: run.ID, ConversationID: run.ConversationID})
	read, err := readBinding.Handler(ctx, json.RawMessage(`{"artifact_id":"`+artifact.ID+`"}`))
	if err != nil || !read.(domain.ToolArtifactRead).Complete {
		t.Fatalf("default bounded read failed: result=%#v err=%v", read, err)
	}
	if _, err := readBinding.Handler(ctx, json.RawMessage(`{"artifact_id":"missing"}`)); !store.IsNotFound(err) {
		t.Fatalf("missing artifact read error = %v", err)
	}
}

func TestArtifactSearchRecordsExpiredAccess(t *testing.T) {
	fileStore, run, artifact := artifactFixture(t, time.Now().UTC().Add(-time.Minute))
	service := NewService(fileStore, eventpkg.NewRecorder(fileStore))
	ctx := eventpkg.WithScope(context.Background(), eventpkg.Scope{RunID: run.ID, ConversationID: run.ConversationID})
	_, err := service.searchBinding().Handler(ctx, json.RawMessage(`{"artifact_id":"`+artifact.ID+`","query":"needle"}`))
	if !errors.Is(err, store.ErrToolArtifactExpired) {
		t.Fatalf("expired search error = %v", err)
	}
	events, err := fileStore.ListRunEvents(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != domain.EventArtifactExpired || events[0].Payload["operation"] != "search" {
		t.Fatalf("expired search event = %#v", events)
	}
}

func artifactFixture(t *testing.T, expires time.Time) (*store.FileStore, domain.Run, domain.ToolArtifact) {
	t.Helper()
	fileStore, err := store.NewFileStore(filepath.Join(t.TempDir(), "agentflow.json"))
	if err != nil {
		t.Fatal(err)
	}
	conversation, _ := fileStore.CreateConversation("artifact tools")
	run, err := fileStore.CreateRunWithContract("agent_planner", conversation.ID, domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte(`{"items":["alpha","needle","omega"]}`)
	sum := sha256.Sum256(content)
	artifact := domain.ToolArtifact{
		ID: "tool_artifact_fixture", SchemaVersion: domain.CurrentToolArtifactSchemaVersion,
		RunID: run.ID, ToolCallID: "source-call", ToolName: "future_tool", MediaType: "application/json",
		ContentHash: "sha256:" + hex.EncodeToString(sum[:]), OriginalByteSize: len(content), StoredByteSize: len(content),
		CreatedAt: time.Now().UTC(), ExpiresAt: &expires,
	}
	if _, err := fileStore.CreateToolArtifact(artifact, content); err != nil {
		t.Fatal(err)
	}
	return fileStore, run, artifact
}
