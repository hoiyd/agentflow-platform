package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/domain"
)

type updateTrace struct {
	mu       sync.Mutex
	updates  []domain.ToolProgressUpdate
	requests []ExecutionRequest
	finished bool
}

func (*updateTrace) ToolStarted(context.Context, ExecutionRequest)   {}
func (s *updateTrace) ToolFinished(context.Context, ExecutionResult) { s.finished = true }
func (s *updateTrace) ToolProgressUpdated(_ context.Context, request ExecutionRequest, update domain.ToolProgressUpdate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, request)
	s.updates = append(s.updates, update)
}

// Failure inventory: invalid input is refused; bursts are coalesced; text is
// redacted and UTF-8 bounded; cancellation/timeout/return revoke retained sinks.
// Progress must not replace either the final result or the typed failure.
func TestExecutionProgressBoundsAndLifecycle(t *testing.T) {
	for _, outcome := range []string{"success", "error", "panic", "cancel", "timeout", "silent"} {
		t.Run(outcome, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				trace := &updateTrace{}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var retained context.Context
				catalog, err := NewCatalog(Binding{
					Descriptor: Descriptor{Name: "long_task", Parameters: ObjectSchema(nil, nil)},
					Policy:     ExecutionPolicy{Timeout: time.Second},
					Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
						retained = ctx
						if outcome == "silent" {
							return "final only", nil
						}
						for _, invalid := range []domain.ToolProgressUpdate{
							{}, {Phase: "not a phase"}, {Phase: "read", Completed: new(int64(2))},
							{Phase: "read", Completed: new(int64(3)), Total: new(int64(2))},
							{Phase: "read", Completed: new(int64(-1)), Total: new(int64(2))},
						} {
							if ReportProgress(ctx, invalid) {
								t.Error("accepted invalid progress")
							}
						}
						if !ReportProgress(ctx, domain.ToolProgressUpdate{Phase: "preparing", Message: "token sk-fixture-123456789012345678901234 preparing"}) {
							t.Error("first update refused")
						}
						time.Sleep(time.Millisecond)
						for i := range 500 {
							ReportProgress(ctx, domain.ToolProgressUpdate{Phase: "reading", Message: strings.Repeat("中", 2000), Completed: new(int64(i)), Total: new(int64(500))})
						}
						time.Sleep(300 * time.Millisecond)
						switch outcome {
						case "error":
							return nil, errors.New("handler failed")
						case "panic":
							panic("handler panic")
						case "cancel":
							cancel()
							<-ctx.Done()
							return nil, ctx.Err()
						case "timeout":
							<-ctx.Done()
							return nil, ctx.Err()
						}
						return "canonical final result", nil
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				result := NewExecutor(catalog, ExecutorOptions{Tracer: trace}).Execute(ctx, ExecutionRequest{Tool: "long_task", RunID: "run", TurnID: "turn", CallID: "call"})
				synctest.Wait()
				if !trace.finished {
					t.Fatal("missing terminal trace")
				}
				if ReportProgress(retained, domain.ToolProgressUpdate{Phase: "late"}) {
					t.Fatal("accepted late update")
				}
				if outcome == "silent" {
					if len(trace.updates) != 0 || result.Result != "final only" {
						t.Fatal("changed no-progress binding")
					}
					return
				}
				if len(trace.updates) < 2 || len(trace.updates) > 4 {
					t.Fatalf("burst not coalesced: %d", len(trace.updates))
				}
				for i, update := range trace.updates {
					if !utf8.ValidString(update.Message) || len(update.Message) > MaxProgressMessageBytes || strings.Contains(update.Message, "sk-fixture") {
						t.Fatalf("unsafe progress: %#v", update)
					}
					if trace.requests[i].CallID != "call" || trace.requests[i].RunID != "run" || trace.requests[i].TurnID != "turn" {
						t.Fatal("lost executor identity")
					}
				}
				if !trace.updates[len(trace.updates)-1].Truncated {
					t.Fatal("missing truncation marker")
				}
				if outcome == "success" {
					if result.Error != nil || result.Result != "canonical final result" {
						t.Fatalf("progress replaced result: %#v", result)
					}
					return
				}
				want := ErrorExecutionFailed
				if outcome == "cancel" {
					want = ErrorExecutionCanceled
				}
				if outcome == "timeout" {
					want = ErrorExecutionTimeout
				}
				if result.Error == nil || result.Error.Code != want {
					t.Fatalf("want %s: %#v", want, result)
				}
			})
		})
	}
	if ReportProgress(context.Background(), domain.ToolProgressUpdate{Phase: "read"}) {
		t.Fatal("accepted update outside Executor")
	}
}

func TestExecutionProgressBoundedSlowConsumer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, sink := newExecutionProgress(context.Background())
		for i := range 2000 {
			ReportProgress(ctx, domain.ToolProgressUpdate{Phase: "reading", Message: strings.Repeat("x", 2000), Completed: new(int64(i)), Total: new(int64(2000))})
		}
		if !sink.publish(func(domain.ToolProgressUpdate) {}) {
			t.Fatal("lost latest bounded update")
		}
		for range 100 {
			ReportProgress(ctx, domain.ToolProgressUpdate{Phase: "reading", Message: strings.Repeat("x", 2000)})
			sink.publish(func(domain.ToolProgressUpdate) {})
		}
		if sink.messages > MaxProgressMessages || sink.bytes > MaxProgressBytes {
			t.Fatalf("unbounded output: %#v", sink)
		}
		sink.close()
		if ReportProgress(ctx, domain.ToolProgressUpdate{Phase: "late"}) {
			t.Fatal("closed producer accepted")
		}
	})
}
