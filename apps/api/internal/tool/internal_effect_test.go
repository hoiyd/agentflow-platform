package tool

import (
	"context"
	"encoding/json"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/tool/policy"
	"agentflow-platform/apps/api/internal/tool/progress"
)

func TestInternalWriteRequiresJournalAndExecutionIdentity(t *testing.T) {
	capability := policy.Capability{SideEffect: policy.SideEffectInternalWrite}
	catalog, err := NewCatalogWithPolicy(policy.Policy{Version: "test", DefaultAction: policy.ActionAllow,
		Rules: []policy.Rule{{ID: "test-write", Tool: "writer", Action: policy.ActionAllow, Capability: capability}}}, Binding{
		Descriptor: Descriptor{Name: "writer", Parameters: ObjectSchema(nil, nil), SideEffect: SideEffectPolicy{Mode: "internal"}, Security: capability},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			t.Error("unguarded write executed")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	base := ExecutionRequest{RunID: "run", TurnID: "turn", CallID: "call", Tool: "writer"}
	for _, test := range []struct {
		name    string
		change  func(*ExecutionRequest)
		journal bool
	}{
		{"no journal", func(*ExecutionRequest) {}, false},
		{"no run", func(r *ExecutionRequest) { r.RunID = "" }, true},
		{"no owner", func(r *ExecutionRequest) { r.TurnID = " " }, true},
		{"no call", func(r *ExecutionRequest) { r.CallID = "" }, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := base
			test.change(&request)
			options := ExecutorOptions{}
			if test.journal {
				options.EffectJournal = &memoryEffectJournal{records: map[string]domain.ToolEffectRecord{}}
			}
			result := NewExecutor(catalog, options).Execute(context.Background(), request)
			if result.Error == nil || result.Error.Code != ErrorIdempotencyRequired {
				t.Fatalf("expected identity/journal rejection, got %#v", result)
			}
		})
	}
}

func TestInternalWriteCountsAsProgressNotReadOnly(t *testing.T) {
	executor := &Executor{progressGuard: progress.New(progress.DefaultConfig())}
	request := ExecutionRequest{Tool: "writer", Arguments: json.RawMessage(`{}`)}
	binding := Binding{Descriptor: Descriptor{Concurrency: ConcurrencyPolicy{Mode: ConcurrencyReadOnly}, SideEffect: SideEffectPolicy{Mode: "internal"}}}
	call := executor.progressCall(request, binding)
	for range 5 {
		result := ExecutionResult{encodedResult: []byte(`"same receipt"`)}
		executor.observeProgress(context.Background(), request, binding, call, &result)
		if result.ProgressDecision == nil || result.ProgressDecision.Trackable || result.ProgressWarning != "" {
			t.Fatalf("committed write classified as stagnant read: %#v", result.ProgressDecision)
		}
	}
}
