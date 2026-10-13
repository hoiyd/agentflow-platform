//go:build linux || darwin

package filesystem

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tool"
)

type missingTerminalEvents struct{ *store.PostgresStore }

func (s missingTerminalEvents) CreateRunEvent(item domain.RunEvent) (domain.RunEvent, error) {
	if item.Type == domain.EventToolCompleted {
		return domain.RunEvent{}, os.ErrPermission
	}
	return s.PostgresStore.CreateRunEvent(item)
}

func TestFilesystemWriteLostTerminalEventDoesNotReplayWrite(t *testing.T) {
	f := newWriteFixture(t)
	executor := tool.NewExecutor(f.catalog, tool.ExecutorOptions{EffectJournal: f.store, Tracer: event.NewToolExecutionTracer(event.NewRecorder(missingTerminalEvents{f.store}), f.run.ID, "")})
	request := tool.ExecutionRequest{RunID: f.run.ID, TurnID: "write-turn", CallID: "lost-event", Tool: "file_write", Arguments: json.RawMessage(`{"root":"output","path":"report.md","content":"Durable event-loss evidence"}`)}
	first := executor.Execute(f.ctx, request)
	if first.Error != nil {
		t.Fatal(first)
	}
	if records := mustWriteEffects(t, f); records[0].Status != domain.ToolEffectCommitted {
		t.Fatal(records)
	}
	events, err := f.store.ListRunEvents(f.run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range events {
		if item.Type == domain.EventToolCompleted {
			t.Fatal("fixture did not drop terminal event")
		}
	}
	if err = event.ValidateLifecycle(events); err == nil {
		t.Fatal("lost terminal event went unnoticed")
	}
	repair, err := event.PlanInterruptedLifecycleRepair(events, "Tool terminal persistence failed")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range repair {
		if _, err = f.store.CreateRunEvent(item); err != nil {
			t.Fatal(err)
		}
	}
	if err = event.ValidateLifecycle(append(events, repair...)); err != nil {
		t.Fatal(err)
	}
	result := f.execute(t, "file_write", "lost-event", string(request.Arguments))
	if result.Error != nil || !result.Replayed {
		t.Fatal(result)
	}
	file, err := os.Open(filepath.Join(f.root, "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	identity, err := fileIdentity(file)
	if err != nil {
		t.Fatal(err)
	}
	if first.Result.(WriteResult).FileID != identity {
		t.Fatal("lost event caused report replacement")
	}
}

type failedIntentStore struct{ *store.PostgresStore }

func (failedIntentStore) SaveToolEffectIntent(string, []byte) error { return os.ErrPermission }

func TestFilesystemWriteIntentFailurePreservesTarget(t *testing.T) {
	f := newWriteFixture(t)
	writeFixture(t, filepath.Join(f.root, "report.md"), "Original")
	service, err := New(f.config, failedIntentStore{f.store})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := tool.NewCatalogWithPolicy(f.catalog.SecurityPolicy(), service.Bindings()...)
	if err != nil {
		t.Fatal(err)
	}
	executor := tool.NewExecutor(catalog, tool.ExecutorOptions{EffectJournal: f.store, Tracer: event.NewToolExecutionTracer(event.NewRecorder(f.store), f.run.ID, "")})
	result := executor.Execute(f.ctx, tool.ExecutionRequest{RunID: f.run.ID, TurnID: "write-turn", CallID: "intent-failed", Tool: "file_write", Arguments: json.RawMessage(`{"root":"output","path":"report.md","content":"Replacement","expected_hash":` + quoted(contentHash([]byte("Original"))) + `}`)})
	if result.Error == nil || result.Error.Code != tool.ErrorEffectJournal || !result.EffectNotApplied {
		t.Fatal(result)
	}
	if data, _ := os.ReadFile(filepath.Join(f.root, "report.md")); string(data) != "Original" {
		t.Fatal("write published without intent")
	}
	if records := mustWriteEffects(t, f); records[0].Status != domain.ToolEffectFailed {
		t.Fatal(records)
	}
}
