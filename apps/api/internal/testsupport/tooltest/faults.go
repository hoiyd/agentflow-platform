package tooltest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/tools"
)

// RunExecutorFaultHarness is the backend-neutral failure matrix every Tool
// Executor implementation must satisfy. Fixtures are local and deterministic;
// no case depends on a model provider, network, clock sleep, or external Tool.
func RunExecutorFaultHarness(t *testing.T) {
	t.Helper()
	tests := []struct {
		name       string
		binding    tools.Binding
		arguments  json.RawMessage
		context    func() (context.Context, context.CancelFunc)
		options    tools.ExecutorOptions
		wantCode   tools.ErrorCode
		wantCutoff bool
	}{
		{
			name: "invalid arguments", binding: successBinding("invalid_arguments"), arguments: json.RawMessage(`[]`),
			wantCode: tools.ErrorInvalidArgs,
		},
		{
			name: "handler error", binding: bindingWithHandler("handler_error", func(context.Context, json.RawMessage) (any, error) {
				return nil, errors.New("handler failed")
			}), wantCode: tools.ErrorExecutionFailed,
		},
		{
			name: "panic", binding: bindingWithHandler("panic", func(context.Context, json.RawMessage) (any, error) {
				panic("fault fixture")
			}), wantCode: tools.ErrorExecutionFailed,
		},
		{
			name: "non JSON result", binding: bindingWithHandler("non_json", func(context.Context, json.RawMessage) (any, error) {
				return make(chan struct{}), nil
			}), wantCode: tools.ErrorResultEncoding,
		},
		{
			name: "oversized result", binding: tools.Binding{
				Descriptor: tools.Descriptor{Name: "oversized", Parameters: tools.ObjectSchema(nil, nil)},
				Handler: func(context.Context, json.RawMessage) (any, error) {
					return map[string]any{"value": strings.Repeat("x", 128)}, nil
				},
				Policy: tools.ExecutionPolicy{MaxResultBytes: 32},
			}, wantCutoff: true,
		},
		{
			name: "timeout", binding: tools.Binding{
				Descriptor: tools.Descriptor{Name: "timeout", Parameters: tools.ObjectSchema(nil, nil)},
				Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
					<-ctx.Done()
					return nil, ctx.Err()
				},
				Policy: tools.ExecutionPolicy{Timeout: time.Millisecond},
			}, wantCode: tools.ErrorExecutionTimeout,
		},
		{
			name: "canceled", binding: bindingWithHandler("canceled", func(ctx context.Context, _ json.RawMessage) (any, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			}),
			context: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, func() {}
			}, wantCode: tools.ErrorExecutionCanceled,
		},
		{
			name: "budget denied", binding: successBinding("budget_denied"),
			context: func() (context.Context, context.CancelFunc) {
				ctx := budget.WithController(context.Background(), deniedBudgetController{})
				return ctx, func() {}
			}, wantCode: tools.ErrorBudgetExceeded,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog, err := tools.NewCatalog(test.binding)
			if err != nil {
				t.Fatalf("new catalog: %v", err)
			}
			tracer := &recordingTracer{}
			test.options.Tracer = tracer
			executor := tools.NewExecutor(catalog, test.options)
			ctx, cancel := context.Background(), func() {}
			if test.context != nil {
				ctx, cancel = test.context()
			}
			defer cancel()
			result := executor.Execute(ctx, tools.ExecutionRequest{
				CallID: "fault-call", Tool: test.binding.Descriptor.Name, Arguments: test.arguments,
			})
			if test.wantCode != "" {
				AssertTypedFailure(t, result, test.wantCode)
			}
			if test.wantCutoff && (!result.Truncated || result.Error != nil || result.OriginalResultBytes <= 32) {
				t.Fatalf("oversized result was not bounded: %#v", result)
			}
			if tracer.starts.Load() != 1 || tracer.finishes.Load() != 1 {
				t.Fatalf("fault trace is not paired: starts=%d finishes=%d", tracer.starts.Load(), tracer.finishes.Load())
			}
		})
	}
}

func successBinding(name string) tools.Binding {
	return bindingWithHandler(name, func(context.Context, json.RawMessage) (any, error) {
		return map[string]any{"ok": true}, nil
	})
}

func bindingWithHandler(name string, handler tools.Handler) tools.Binding {
	return tools.Binding{
		Descriptor: tools.Descriptor{Name: name, Parameters: tools.ObjectSchema(nil, nil)},
		Handler:    handler,
	}
}

type deniedBudgetController struct{}

func (deniedBudgetController) BeginModelCall(context.Context, budget.ModelCallEstimate) (budget.ModelReservation, error) {
	return budget.ModelReservation{}, nil
}

func (deniedBudgetController) SettleModelCall(context.Context, budget.ModelReservation, budget.ModelUsage) error {
	return nil
}

func (deniedBudgetController) RecordToolCall(context.Context, budget.ToolCall) error {
	return &budget.ExceededError{Resource: budget.ResourceToolCalls, Limit: 1, Used: 1, Requested: 1}
}

var _ budget.Controller = deniedBudgetController{}

func AssertTypedFailure(t *testing.T, result tools.ExecutionResult, code tools.ErrorCode) {
	t.Helper()
	if err := validateTypedFailure(result, code); err != nil {
		t.Fatal(err)
	}
}

func validateTypedFailure(result tools.ExecutionResult, code tools.ErrorCode) error {
	if result.Error == nil || result.Error.Code != code {
		return fmt.Errorf("error = %#v, want %s", result.Error, code)
	}
	info := result.Error.FailureInfo()
	if info.Code != string(code) || info.Source != "tool" || info.Category == "" {
		return fmt.Errorf("failure classification is incomplete: %#v", info)
	}
	return nil
}
