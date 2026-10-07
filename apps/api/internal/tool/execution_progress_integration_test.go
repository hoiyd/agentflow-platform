package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/projection"
	"agentflow-platform/apps/api/internal/testsupport/fixturestore"
	"agentflow-platform/apps/api/internal/tool"
)

type progressStore struct {
	event.RunEventStore
	hub  *event.Hub
	fail bool
}

func (s progressStore) CreateRunEvent(item domain.RunEvent) (domain.RunEvent, error) {
	if s.fail && item.Type == domain.EventToolProgress {
		return domain.RunEvent{}, errors.New("injected progress persistence failure")
	}
	created, err := s.RunEventStore.CreateRunEvent(item)
	if err == nil {
		s.hub.PublishCommitted(created)
	}
	return created, err
}

// Failure inventory: a non-reading subscriber is disconnected, never blocking
// the Handler; event/message/byte budgets apply independently; failed display
// commits never become observable facts or alter the canonical Tool result.
func TestToolProgressBackpressureAndFaultIntegration(t *testing.T) {
	for _, scenario := range []string{"burst", "messages", "bytes", "invalid", "store_failure"} {
		t.Run(scenario, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				storage := fixturestore.New()
				conversation, _ := storage.CreateConversation("progress limits")
				run, err := storage.CreateRunWithContract("agent_planner", conversation.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}}, nil)
				if err != nil {
					t.Fatal(err)
				}
				hub := event.NewHub(1)
				subscription, err := hub.SnapshotAndSubscribe(context.Background(), run.ID, func() (domain.RunProjectionSnapshot, error) {
					return projection.BuildSnapshot(run, nil, domain.RunUsageLedger{}, nil), nil
				})
				if err != nil {
					t.Fatal(err)
				}
				defer subscription.Close()
				tracer := event.NewToolExecutionTracer(event.NewRecorder(progressStore{RunEventStore: storage, hub: hub, fail: scenario == "store_failure"}), run.ID, "")
				ctx := event.WithScope(context.Background(), event.Scope{RunID: run.ID, TurnID: "t", ConversationID: conversation.ID})
				catalog, err := tool.NewCatalog(tool.Binding{Descriptor: tool.Descriptor{Name: "reader", Parameters: tool.ObjectSchema(nil, nil)}, Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
					if scenario == "invalid" {
						for _, invalid := range []domain.ToolProgressUpdate{
							{Phase: "reading", Message: string([]byte{0xff})},
							{Phase: "reading", Message: strings.Repeat("x", 8193)},
							{Phase: "reading", Total: new(int64(2))},
							{Phase: "reading", Completed: new(int64(1)), Total: new(int64(1 << 53))},
						} {
							if tool.ReportProgress(ctx, invalid) {
								t.Error("accepted invalid producer payload")
							}
						}
						// Also exercise the registered event contract, not only the sink.
						tracer.ToolProgressUpdated(ctx, tool.ExecutionRequest{CallID: "c", Tool: "reader"}, domain.ToolProgressUpdate{Phase: "reading", Message: strings.Repeat("x", 1025)})
						return "canonical", nil
					}
					count := 80
					if scenario == "burst" {
						count = 2000
					}
					for i := range count {
						message := ""
						if scenario == "bytes" {
							message = strings.Repeat("中", 333)
						}
						tool.ReportProgress(ctx, domain.ToolProgressUpdate{Phase: "reading", Message: message, Completed: new(int64(i)), Total: new(int64(count))})
						if scenario != "burst" {
							time.Sleep(300 * time.Millisecond)
						}
					}
					return "canonical", nil
				}, Policy: tool.ExecutionPolicy{Timeout: time.Minute}})
				if err != nil {
					t.Fatal(err)
				}
				result := tool.NewExecutor(catalog, tool.ExecutorOptions{Tracer: tracer}).Execute(ctx, tool.ExecutionRequest{RunID: run.ID, TurnID: "t", CallID: "c", Tool: "reader"})
				if result.Error != nil || result.Result != "canonical" {
					t.Fatalf("display affected result: %#v", result)
				}
				events, _ := storage.ListRunEvents(run.ID)
				messages, bytes := 0, 0
				for _, item := range events {
					if item.Type == domain.EventToolProgress {
						messages++
						text, _ := item.Payload["message"].(string)
						bytes += len(text)
						if !utf8.ValidString(text) {
							t.Fatal("corrupt UTF-8 checkpoint")
						}
					}
				}
				if messages > tool.MaxProgressMessages || bytes > tool.MaxProgressBytes {
					t.Fatalf("messages=%d bytes=%d", messages, bytes)
				}
				switch scenario {
				case "messages":
					if messages != tool.MaxProgressMessages {
						t.Fatalf("message limit not exercised: %d", messages)
					}
				case "bytes":
					if bytes < tool.MaxProgressBytes-2 {
						t.Fatalf("byte limit not exercised: %d", bytes)
					}
				case "burst":
					if messages > 2 {
						t.Fatalf("burst not coalesced: %d", messages)
					}
				case "invalid", "store_failure":
					if messages != 0 {
						t.Fatal("invalid/uncommitted progress escaped")
					}
				}
				if err := <-subscription.Errors; !errors.Is(err, event.ErrSubscriberLagged) {
					t.Fatalf("slow subscriber not disconnected: %v", err)
				}
			})
		})
	}
}
