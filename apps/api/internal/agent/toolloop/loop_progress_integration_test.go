package toolloop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/projection"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/tool"
)

// Progress must cross the real loop tracer into persistence, never the model's
// follow-up or answer stream. Single and staged callers share this path.
func TestToolLoopProgressPersistenceIntegration(t *testing.T) {
	for _, stage := range []string{"", "worker", "act"} {
		t.Run("stage="+stage, func(t *testing.T) {
			storage := fixturestore.New()
			conversation, _ := storage.CreateConversation("progress loop")
			run, err := storage.CreateRunWithContract("agent_planner", conversation.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			catalog, err := tool.NewCatalog(tool.Binding{Descriptor: tool.Descriptor{Name: "reader", Parameters: tool.ObjectSchema(nil, nil)}, Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
				if !tool.ReportProgress(ctx, domain.ToolProgressUpdate{Phase: "reading", Message: "DISPLAY_PROGRESS_ONLY"}) {
					t.Error("progress sink unavailable")
				}
				return "canonical observation", nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			ctx := event.WithScope(boundedContext(t), event.Scope{RunID: run.ID, ConversationID: conversation.ID, StageID: stage, TurnID: "loop-turn"})
			model := &modelStub{selected: provider.ChatChoice{ToolCalls: []provider.ToolCall{{ID: "provider-call", Type: "function", Function: provider.FunctionCall{Name: "reader", Arguments: "{}"}}}}}
			events, errs := Stream(ctx, model, Request{Latest: "read", Catalog: catalog, Trace: provider.ChatTrace{RunID: run.ID, StepID: stage, Recorder: event.NewRecorder(storage)}})
			for item := range events {
				if strings.Contains(item.Delta, "DISPLAY_PROGRESS_ONLY") {
					t.Fatal("progress leaked into answer")
				}
			}
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(model.final)
			if strings.Contains(string(data), "DISPLAY_PROGRESS_ONLY") || !strings.Contains(string(data), "canonical observation") {
				t.Fatalf("progress altered model context: %s", data)
			}
			saved, _ := storage.ListRunEvents(run.ID)
			if err := event.ValidateLifecycle(saved); err != nil {
				t.Fatal(err)
			}
			items := projection.BuildSnapshot(run, saved, domain.RunUsageLedger{}, nil).ToolProgress
			if len(items) != 1 || items[0].Status != "completed" || items[0].StageID != stage || items[0].Message != "DISPLAY_PROGRESS_ONLY" {
				t.Fatalf("missing durable progress: %#v", items)
			}
		})
	}
}
