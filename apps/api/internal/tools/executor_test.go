package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestExecutorReturnsTypedErrors(t *testing.T) {
	catalog := DefaultCatalog()
	executor := NewExecutor(catalog, ExecutorOptions{})

	tests := []struct {
		name    string
		request ExecutionRequest
		code    ErrorCode
	}{
		{name: "missing tool", request: ExecutionRequest{Tool: "missing", Arguments: json.RawMessage(`{}`)}, code: ErrorToolNotFound},
		{name: "invalid arguments", request: ExecutionRequest{Tool: "calculator", Arguments: json.RawMessage(`[]`)}, code: ErrorInvalidArgs},
		{name: "handler error", request: ExecutionRequest{Tool: "calculator", Arguments: json.RawMessage(`{"expression":"1 / 0"}`)}, code: ErrorExecutionFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := executor.Execute(context.Background(), test.request)
			if result.Error == nil || result.Error.Code != test.code {
				t.Fatalf("expected %s, got %#v", test.code, result.Error)
			}
		})
	}
}

func TestExecutorAppliesTimeout(t *testing.T) {
	catalog, err := NewCatalog(Binding{
		Descriptor: Descriptor{Name: "slow", Parameters: ObjectSchema(nil, nil)},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
		Policy: ExecutionPolicy{Timeout: 10 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("new catalog: %v", err)
	}
	result := NewExecutor(catalog, ExecutorOptions{}).Execute(context.Background(), ExecutionRequest{Tool: "slow"})
	if result.Error == nil || result.Error.Code != ErrorExecutionTimeout {
		t.Fatalf("expected timeout error, got %#v", result.Error)
	}
	if result.DurationMS <= 0 {
		t.Fatalf("expected recorded duration, got %dms", result.DurationMS)
	}
}

func TestExecutorReportsParentCancellation(t *testing.T) {
	catalog, err := NewCatalog(Binding{
		Descriptor: Descriptor{Name: "blocking", Parameters: ObjectSchema(nil, nil)},
		Handler: func(ctx context.Context, args json.RawMessage) (any, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	if err != nil {
		t.Fatalf("new catalog: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := NewExecutor(catalog, ExecutorOptions{}).Execute(ctx, ExecutionRequest{Tool: "blocking"})
	if result.Error == nil || result.Error.Code != ErrorExecutionCanceled {
		t.Fatalf("expected cancellation error, got %#v", result.Error)
	}
}

func TestExecutorLimitsResultSize(t *testing.T) {
	catalog, err := NewCatalog(Binding{
		Descriptor: Descriptor{Name: "large", Parameters: ObjectSchema(nil, nil)},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			return map[string]any{"value": strings.Repeat("x", 100)}, nil
		},
		Policy: ExecutionPolicy{MaxResultBytes: 32},
	})
	if err != nil {
		t.Fatalf("new catalog: %v", err)
	}
	result := NewExecutor(catalog, ExecutorOptions{}).Execute(context.Background(), ExecutionRequest{Tool: "large"})
	if !result.Truncated || result.OriginalResultBytes <= 32 || result.Error != nil {
		t.Fatalf("expected a truncated result, got %#v", result)
	}
	preview, ok := result.Result.(map[string]any)["preview"].(string)
	if !ok || len([]byte(preview)) > 32 {
		t.Fatalf("expected preview within result limit, got %#v", result.Result)
	}
}

func TestExecutorConvertsPanicsToErrors(t *testing.T) {
	catalog, err := NewCatalog(Binding{
		Descriptor: Descriptor{Name: "panic", Parameters: ObjectSchema(nil, nil)},
		Handler:    func(context.Context, json.RawMessage) (any, error) { panic("boom") },
	})
	if err != nil {
		t.Fatalf("new catalog: %v", err)
	}
	result := NewExecutor(catalog, ExecutorOptions{}).Execute(context.Background(), ExecutionRequest{Tool: "panic"})
	if result.Error == nil || result.Error.Code != ErrorExecutionFailed || !strings.Contains(result.Error.Message, "panicked") {
		t.Fatalf("expected panic execution error, got %#v", result.Error)
	}
}

func TestExecutorRejectsNonJSONResult(t *testing.T) {
	catalog, err := NewCatalog(Binding{
		Descriptor: Descriptor{Name: "invalid_result", Parameters: ObjectSchema(nil, nil)},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			return make(chan struct{}), nil
		},
	})
	if err != nil {
		t.Fatalf("new catalog: %v", err)
	}
	result := NewExecutor(catalog, ExecutorOptions{}).Execute(context.Background(), ExecutionRequest{Tool: "invalid_result"})
	if result.Error == nil || result.Error.Code != ErrorResultEncoding {
		t.Fatalf("expected result encoding error, got %#v", result.Error)
	}
}

func TestExecutorEmitsTracingCallbacks(t *testing.T) {
	tracer := &recordingTracer{}
	result := NewExecutor(DefaultCatalog(), ExecutorOptions{Tracer: tracer}).Execute(
		context.Background(),
		ExecutionRequest{CallID: "call-1", Tool: "calculator", Arguments: json.RawMessage(`{"expression":"1 + 1"}`)},
	)
	if result.Error != nil {
		t.Fatalf("execute: %v", result.Error)
	}
	if tracer.started.CallID != "call-1" || tracer.finished.CallID != "call-1" {
		t.Fatalf("unexpected trace callbacks: %#v %#v", tracer.started, tracer.finished)
	}
}

func TestExecutionErrorUnwrapsCause(t *testing.T) {
	cause := errors.New("cause")
	err := executionError(ErrorExecutionFailed, "failed", cause)
	if !errors.Is(err, cause) {
		t.Fatal("expected execution error to unwrap its cause")
	}
}

type recordingTracer struct {
	started  ExecutionRequest
	finished ExecutionResult
}

func (t *recordingTracer) ToolStarted(_ context.Context, request ExecutionRequest) {
	t.started = request
}

func (t *recordingTracer) ToolFinished(_ context.Context, result ExecutionResult) {
	t.finished = result
}
