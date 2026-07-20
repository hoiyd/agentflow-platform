package event

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tools"
)

func TestToolExecutionTracerRecordsCanceledExecution(t *testing.T) {
	fileStore, err := store.NewFileStore(filepath.Join(t.TempDir(), "agentflow.json"))
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("tool trace")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := fileStore.CreateRun("agent_planner", conversation.ID, domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{},
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	catalog, err := tools.NewCatalog(tools.Binding{
		Descriptor: tools.Descriptor{Name: "blocking", Parameters: tools.ObjectSchema(nil, nil)},
		Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	if err != nil {
		t.Fatalf("new catalog: %v", err)
	}
	executor := tools.NewExecutor(catalog, tools.ExecutorOptions{
		Tracer: NewToolExecutionTracer(NewRecorder(fileStore), run.ID, ""),
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := executor.Execute(ctx, tools.ExecutionRequest{CallID: "call-1", Tool: "blocking"})
	if result.Error == nil || result.Error.Code != tools.ErrorExecutionCanceled {
		t.Fatalf("expected cancellation error, got %#v", result.Error)
	}

	events, err := fileStore.ListRunEvents(run.ID)
	if err != nil {
		t.Fatalf("list run events: %v", err)
	}
	if len(events) != 2 || events[0].Type != domain.EventToolStarted || events[1].Type != domain.EventToolFailed {
		t.Fatalf("expected tool started and failed events, got %#v", events)
	}
	if events[1].Payload["error_code"] != string(tools.ErrorExecutionCanceled) {
		t.Fatalf("unexpected error code payload: %#v", events[1].Payload)
	}
}
