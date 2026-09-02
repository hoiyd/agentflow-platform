package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"agentflow-platform/apps/api/internal/toolprogress"
)

type progressTrace struct {
	decisions []toolprogress.Decision
}

func (*progressTrace) ToolStarted(context.Context, ExecutionRequest) {}
func (*progressTrace) ToolFinished(context.Context, ExecutionResult) {}
func (t *progressTrace) ToolProgressEvaluated(_ context.Context, _ ExecutionRequest, decision toolprogress.Decision) {
	t.decisions = append(t.decisions, decision)
}

func TestExecutorProgressGuardWarnsBlocksAndHaltsRepeatedFailure(t *testing.T) {
	handlerCalls := 0
	catalog, err := NewCatalog(Binding{
		Descriptor: Descriptor{
			Name: "unstable_lookup", Parameters: ObjectSchema(nil, nil),
			Concurrency: ConcurrencyPolicy{Mode: ConcurrencyReadOnly},
		},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			handlerCalls++
			return nil, errors.New("service unavailable")
		},
	})
	if err != nil {
		t.Fatalf("new catalog: %v", err)
	}
	trace := &progressTrace{}
	executor := NewExecutor(catalog, ExecutorOptions{ProgressGuard: toolprogress.New(toolprogress.DefaultConfig()), Tracer: trace})
	request := ExecutionRequest{CallID: "call", Tool: "unstable_lookup", Arguments: json.RawMessage(`{}`)}

	for attempt := 1; attempt <= 5; attempt++ {
		request.CallID = "call-" + string(rune('0'+attempt))
		result := executor.Execute(context.Background(), request)
		switch attempt {
		case 1:
			if result.ProgressWarning != "" || result.Error == nil || result.Error.Code != ErrorExecutionFailed {
				t.Fatalf("first result=%#v", result)
			}
		case 2, 3:
			if result.ProgressWarning == "" || result.Error == nil || result.Error.Code != ErrorExecutionFailed {
				t.Fatalf("warning result %d=%#v", attempt, result)
			}
		case 4:
			if result.Error == nil || result.Error.Code != ErrorProgressBlocked || result.ProgressDecision.Action != toolprogress.ActionBlockCall {
				t.Fatalf("blocked result=%#v", result)
			}
		case 5:
			if result.Error == nil || result.Error.Code != ErrorNoProgress || result.ProgressDecision.Action != toolprogress.ActionHaltTurn {
				t.Fatalf("halt result=%#v", result)
			}
		}
	}
	if handlerCalls != 3 {
		t.Fatalf("handler calls=%d, want 3", handlerCalls)
	}
	if len(trace.decisions) != 4 || trace.decisions[0].Action != toolprogress.ActionWarn || trace.decisions[len(trace.decisions)-1].Action != toolprogress.ActionHaltTurn {
		t.Fatalf("unexpected trace decisions: %#v", trace.decisions)
	}
}

func TestExecutorProgressGuardTracksReadOnlyOnlyAndNormalizesFallbackArguments(t *testing.T) {
	serialCalls := 0
	catalog, err := NewCatalog(Binding{
		Descriptor: Descriptor{Name: "serial_compute", Parameters: ObjectSchema(map[string]any{
			"value": map[string]any{"type": "number"},
		}, []string{"value"})},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			serialCalls++
			return map[string]any{"same": true}, nil
		},
	})
	if err != nil {
		t.Fatalf("new catalog: %v", err)
	}
	executor := NewExecutor(catalog, ExecutorOptions{ProgressGuard: toolprogress.New(toolprogress.DefaultConfig())})
	for range 6 {
		result := executor.Execute(context.Background(), ExecutionRequest{Tool: "serial_compute", Arguments: json.RawMessage(`{"value":1}`)})
		if result.Error != nil || result.ProgressDecision == nil || result.ProgressDecision.Trackable {
			t.Fatalf("serial result should remain untracked: %#v", result)
		}
	}
	if serialCalls != 6 {
		t.Fatalf("serial handler calls=%d", serialCalls)
	}
	if fallbackArgumentsHash(json.RawMessage(" { \"a\" : 1 } ")) != fallbackArgumentsHash(json.RawMessage(`{"a":1}`)) {
		t.Fatal("semantically identical fallback arguments produced different hashes")
	}
}

func TestProgressTrackableErrorIsConservative(t *testing.T) {
	for _, code := range []ErrorCode{ErrorToolNotFound, ErrorInvalidArgs, ErrorExecutionFailed, ErrorExecutionTimeout, ErrorResultEncoding} {
		if !progressTrackableError(code) {
			t.Fatalf("expected %s to be trackable", code)
		}
	}
	for _, code := range []ErrorCode{ErrorExecutionCanceled, ErrorBudgetExceeded, ErrorEffectReconciliation, ErrorSecurityPolicyDenied, ErrorNoProgress} {
		if progressTrackableError(code) {
			t.Fatalf("expected %s to be excluded", code)
		}
	}
}
