package event

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/projection"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/tool"
)

// Failure inventory: no invented Stage in Single, foreign/closed call updates
// refused, terminal status from events not percentages, and no post-crash claim
// of running execution. Persisted updates must rebuild after Store reopen.
func TestToolProgressCommittedProjection(t *testing.T) {
	for _, stage := range []string{"", "worker", "act"} {
		t.Run("stage="+stage, func(t *testing.T) {
			store := fixturestore.New()
			conversation, _ := store.CreateConversation("progress")
			run, err := store.CreateRunWithContract("agent_planner", conversation.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx := WithScope(context.Background(), Scope{RunID: run.ID, ConversationID: conversation.ID, StageID: stage, TurnID: "turn"})
			tracer := NewToolExecutionTracer(NewRecorder(store), run.ID, stage)
			request := tool.ExecutionRequest{RunID: run.ID, StageID: stage, TurnID: "turn", CallID: "call", Tool: "reader"}
			catalog, err := tool.NewCatalog(tool.Binding{Descriptor: tool.Descriptor{Name: "reader", Parameters: tool.ObjectSchema(nil, nil)}, Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
				foreign := request
				foreign.RunID = "foreign-run"
				tracer.ToolProgressUpdated(ctx, foreign, domain.ToolProgressUpdate{Phase: "foreign"})
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				tracer.ToolProgressUpdated(canceled, request, domain.ToolProgressUpdate{Phase: "canceled"})
				tool.ReportProgress(ctx, domain.ToolProgressUpdate{Phase: "reading", Message: "token sk-fixture-123456789012345678901234 done", Completed: new(int64(1)), Total: new(int64(1))})
				return "only final observation", nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			result := tool.NewExecutor(catalog, tool.ExecutorOptions{Tracer: tracer}).Execute(ctx, request)
			if result.Error != nil || result.Result != "only final observation" {
				t.Fatalf("result: %#v", result)
			}
			tracer.ToolProgressUpdated(ctx, request, domain.ToolProgressUpdate{Phase: "late"})
			events, _ := store.ListRunEvents(run.ID)
			if err := ValidateLifecycle(events); err != nil {
				t.Fatalf("committed lifecycle: %v", err)
			}
			corrupt := append([]domain.RunEvent(nil), events...)
			for index, item := range corrupt {
				if item.Type == domain.EventToolProgress {
					corrupt[index].StageID = "foreign-stage"
				}
			}
			if err := ValidateLifecycle(corrupt); err == nil || !strings.Contains(err.Error(), "matching active call scope") {
				t.Fatalf("foreign progress scope accepted: %v", err)
			}
			for _, payload := range []map[string]any{
				{"phase": "reading", "tool_name": "reader"},
				{"phase": "reading", "tool_call_id": "call", "tool_name": "reader", "completed": "wrong type"},
				{"phase": "reading", "tool_call_id": "call", "tool_name": "reader", "unsupported": make(chan int)},
			} {
				if _, err := store.CreateRunEvent(domain.RunEvent{RunID: run.ID, TurnID: "turn", StageID: stage, Type: domain.EventToolProgress, Payload: payload}); err == nil {
					t.Fatal("malformed progress persisted")
				}
			}
			items := projection.BuildSnapshot(run, events, domain.RunUsageLedger{}, nil).ToolProgress
			if len(items) != 1 || items[0].Status != "completed" || items[0].Phase != "reading" || items[0].StageID != stage || items[0].ToolCallID != "call" || items[0].TurnID != "turn" {
				t.Fatalf("projection: %#v", items)
			}
			if items[0].Message != "token [REDACTED] done" {
				t.Fatalf("unredacted progress: %q", items[0].Message)
			}
			for _, item := range events {
				if item.Type == domain.EventToolProgress {
					if item.StageID != stage || item.TurnID != "turn" {
						t.Fatal("identity drift")
					}
				}
			}
			// A saved progress update never becomes evidence that an interrupted
			// worker completed. Recovery uses the original lifecycle repair.
			prefix := []domain.RunEvent{}
			for _, item := range events {
				if item.Type != domain.EventToolCompleted {
					prefix = append(prefix, item)
				}
			}
			run.Status = domain.RunFailedRecoverable
			items = projection.BuildSnapshot(run, prefix, domain.RunUsageLedger{}, nil).ToolProgress
			if len(items) != 1 || items[0].Status != "interrupted" {
				t.Fatalf("crash view: %#v", items)
			}
		})
	}
}
