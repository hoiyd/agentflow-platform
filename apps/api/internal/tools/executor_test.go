package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/domain"
)

func TestExecutorChargesOnlyValidatedToolExecutions(t *testing.T) {
	controller := &toolBudgetController{}
	ctx := budget.WithController(context.Background(), controller)
	executor := NewExecutor(DefaultCatalog(), ExecutorOptions{})

	invalid := executor.Execute(ctx, ExecutionRequest{CallID: "invalid", Tool: "calculator", Arguments: json.RawMessage(`[]`)})
	if invalid.Error == nil || invalid.Error.Code != ErrorInvalidArgs || controller.calls != 0 {
		t.Fatalf("invalid arguments consumed tool budget: result=%#v calls=%d", invalid, controller.calls)
	}
	valid := executor.Execute(ctx, ExecutionRequest{CallID: "valid", Tool: "calculator", Arguments: json.RawMessage(`{"expression":"1 + 1"}`)})
	if valid.Error != nil || controller.calls != 1 || controller.last.OperationID != "valid" || controller.last.ToolName != "calculator" || controller.last.Purpose != domain.UsagePurposePrimary {
		t.Fatalf("validated execution was not charged once: result=%#v controller=%#v", valid, controller)
	}
}

func TestExecutorReturnsTypedBudgetError(t *testing.T) {
	controller := &toolBudgetController{err: &budget.ExceededError{Resource: budget.ResourceToolCalls, Limit: 1, Used: 1, Requested: 1}}
	ctx := budget.WithController(context.Background(), controller)
	result := NewExecutor(DefaultCatalog(), ExecutorOptions{}).Execute(ctx, ExecutionRequest{CallID: "call-2", Tool: "calculator", Arguments: json.RawMessage(`{"expression":"2 + 2"}`)})
	if result.Error == nil || result.Error.Code != ErrorBudgetExceeded {
		t.Fatalf("expected typed budget error, got %#v", result.Error)
	}
}

type toolBudgetController struct {
	calls int
	last  budget.ToolCall
	err   error
}

func (c *toolBudgetController) BeginModelCall(context.Context, budget.ModelCallEstimate) (budget.ModelReservation, error) {
	return budget.ModelReservation{}, nil
}

func (c *toolBudgetController) SettleModelCall(context.Context, budget.ModelReservation, budget.ModelUsage) error {
	return nil
}

func (c *toolBudgetController) RecordToolCall(_ context.Context, call budget.ToolCall) error {
	c.calls++
	c.last = call
	return c.err
}

var _ budget.Controller = (*toolBudgetController)(nil)

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

func TestExecutorReplaysCommittedSideEffectWithoutInvokingHandler(t *testing.T) {
	var calls atomic.Int32
	catalog, err := newExternalTestCatalog(Binding{
		Descriptor: Descriptor{
			Name: "write_record", Parameters: ObjectSchema(map[string]any{
				"value": map[string]any{"type": "string"},
			}, []string{"value"}),
			SideEffect: SideEffectPolicy{Mode: SideEffectExternal},
		},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			calls.Add(1)
			return map[string]any{"record_id": "record-1"}, nil
		},
	})
	if err != nil {
		t.Fatalf("new catalog: %v", err)
	}
	journal := &memoryEffectJournal{records: make(map[string]domain.ToolEffectRecord)}
	executor := NewExecutor(catalog, ExecutorOptions{EffectJournal: journal})
	request := ExecutionRequest{
		CallID: "call-1", RunID: "run-1", StageID: "stage-1", TurnID: "turn-1",
		Tool: "write_record", Arguments: json.RawMessage(`{"value":"a"}`),
	}
	first := executor.Execute(context.Background(), request)
	second := executor.Execute(context.Background(), request)
	if first.Error != nil || second.Error != nil {
		t.Fatalf("execute side effect: first=%#v second=%#v", first.Error, second.Error)
	}
	if calls.Load() != 1 || !second.Replayed {
		t.Fatalf("expected one handler call and a replay, calls=%d second=%#v", calls.Load(), second)
	}
	if second.ArgumentsHash == "" || second.DefinitionRevision == "" {
		t.Fatalf("replayed result lost contract identity: %#v", second)
	}
	if second.Result.(map[string]any)["record_id"] != "record-1" {
		t.Fatalf("unexpected replayed result: %#v", second.Result)
	}
}

func TestExecutorFailsClosedForUncertainSideEffect(t *testing.T) {
	catalog, err := newExternalTestCatalog(Binding{
		Descriptor: Descriptor{Name: "write_record", Parameters: ObjectSchema(nil, nil), SideEffect: SideEffectPolicy{Mode: SideEffectExternal}},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			t.Fatal("uncertain side effect must not execute again")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := ExecutionRequest{CallID: "call-1", RunID: "run-1", StageID: "stage-1", Tool: "write_record", Arguments: json.RawMessage(`{}`)}
	key := sideEffectKey(request)
	journal := &memoryEffectJournal{records: map[string]domain.ToolEffectRecord{
		key: {IdempotencyKey: key, RunID: request.RunID, StageID: request.StageID, ToolCallID: request.CallID, ToolName: request.Tool, RequestHash: sideEffectRequestHash(request), Status: domain.ToolEffectExecuting},
	}}
	result := NewExecutor(catalog, ExecutorOptions{EffectJournal: journal}).Execute(context.Background(), request)
	if result.Error == nil || result.Error.Code != ErrorEffectReconciliation {
		t.Fatalf("expected reconciliation error, got %#v", result.Error)
	}
}

func TestExecutorRequiresJournalAndExecutionIdentityForExternalSideEffects(t *testing.T) {
	catalog, err := newExternalTestCatalog(Binding{
		Descriptor: Descriptor{Name: "writer", Parameters: ObjectSchema(nil, nil), SideEffect: SideEffectPolicy{Mode: SideEffectExternal}},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			t.Fatal("invalid side effect must not execute")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := ExecutionRequest{CallID: "call-1", RunID: "run-1", StageID: "stage-1", Tool: "writer", Arguments: json.RawMessage(`{}`)}
	withoutJournal := NewExecutor(catalog, ExecutorOptions{}).Execute(context.Background(), request)
	if withoutJournal.Error == nil || withoutJournal.Error.Code != ErrorIdempotencyRequired {
		t.Fatalf("expected journal requirement, got %#v", withoutJournal.Error)
	}
	missingIdentity := request
	missingIdentity.StageID = ""
	result := NewExecutor(catalog, ExecutorOptions{EffectJournal: &memoryEffectJournal{records: make(map[string]domain.ToolEffectRecord)}}).Execute(context.Background(), missingIdentity)
	if result.Error == nil || result.Error.Code != ErrorIdempotencyRequired {
		t.Fatalf("expected identity requirement, got %#v", result.Error)
	}
}

func TestExecutorSurfacesSideEffectJournalFailures(t *testing.T) {
	catalog, err := newExternalTestCatalog(Binding{
		Descriptor: Descriptor{Name: "writer", Parameters: ObjectSchema(nil, nil), SideEffect: SideEffectPolicy{Mode: SideEffectExternal}},
		Handler:    func(context.Context, json.RawMessage) (any, error) { return map[string]any{"ok": true}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	request := ExecutionRequest{CallID: "call-1", RunID: "run-1", StageID: "stage-1", Tool: "writer", Arguments: json.RawMessage(`{}`)}
	prepareFailure := &memoryEffectJournal{records: make(map[string]domain.ToolEffectRecord), beginErr: errors.New("prepare failed")}
	result := NewExecutor(catalog, ExecutorOptions{EffectJournal: prepareFailure}).Execute(context.Background(), request)
	if result.Error == nil || result.Error.Code != ErrorEffectJournal {
		t.Fatalf("expected prepare journal error, got %#v", result.Error)
	}

	commitFailure := &memoryEffectJournal{records: make(map[string]domain.ToolEffectRecord), completeErr: errors.New("commit failed")}
	result = NewExecutor(catalog, ExecutorOptions{EffectJournal: commitFailure}).Execute(context.Background(), request)
	if result.Error == nil || result.Error.Code != ErrorEffectJournal || commitFailure.markCalls != 1 {
		t.Fatalf("expected commit journal error and reconciliation mark, result=%#v marks=%d", result.Error, commitFailure.markCalls)
	}
}

func TestExecutorRejectsCorruptCommittedSideEffectResult(t *testing.T) {
	catalog, err := newExternalTestCatalog(Binding{
		Descriptor: Descriptor{Name: "writer", Parameters: ObjectSchema(nil, nil), SideEffect: SideEffectPolicy{Mode: SideEffectExternal}},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			t.Fatal("committed side effect must not execute")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := ExecutionRequest{CallID: "call-1", RunID: "run-1", StageID: "stage-1", Tool: "writer", Arguments: json.RawMessage(`{}`)}
	key := sideEffectKey(request)
	journal := &memoryEffectJournal{records: map[string]domain.ToolEffectRecord{
		key: {
			IdempotencyKey: key, RunID: request.RunID, StageID: request.StageID,
			ToolCallID: request.CallID, ToolName: request.Tool, RequestHash: sideEffectRequestHash(request),
			Status: domain.ToolEffectCommitted, Result: []byte(`not-json`),
		},
	}}
	result := NewExecutor(catalog, ExecutorOptions{EffectJournal: journal}).Execute(context.Background(), request)
	if result.Error == nil || result.Error.Code != ErrorEffectJournal {
		t.Fatalf("expected corrupt journal result error, got %#v", result.Error)
	}
}

func TestExecutorMarksFailedExternalHandlerForReconciliation(t *testing.T) {
	catalog, err := newExternalTestCatalog(Binding{
		Descriptor: Descriptor{Name: "writer", Parameters: ObjectSchema(nil, nil), SideEffect: SideEffectPolicy{Mode: SideEffectExternal}},
		Handler:    func(context.Context, json.RawMessage) (any, error) { return nil, errors.New("remote write uncertain") },
	})
	if err != nil {
		t.Fatal(err)
	}
	journal := &memoryEffectJournal{records: make(map[string]domain.ToolEffectRecord)}
	request := ExecutionRequest{CallID: "call-1", RunID: "run-1", StageID: "stage-1", Tool: "writer", Arguments: json.RawMessage(`{}`)}
	result := NewExecutor(catalog, ExecutorOptions{EffectJournal: journal}).Execute(context.Background(), request)
	if result.Error == nil || result.Error.Code != ErrorExecutionFailed || journal.markCalls != 1 {
		t.Fatalf("expected failed execution and reconciliation mark, result=%#v marks=%d", result.Error, journal.markCalls)
	}
	if journal.records[sideEffectKey(request)].Status != domain.ToolEffectNeedsReconciliation {
		t.Fatalf("unexpected journal state: %#v", journal.records[sideEffectKey(request)])
	}
}

func TestExecuteBatchDefaultsToSerial(t *testing.T) {
	var active atomic.Int32
	var maximum atomic.Int32
	catalog, err := NewCatalog(concurrencyTestTool("serial", ConcurrencyPolicy{}, &active, &maximum))
	if err != nil {
		t.Fatalf("new catalog: %v", err)
	}
	requests := []ExecutionRequest{{Tool: "serial"}, {Tool: "serial"}}
	results := NewExecutor(catalog, ExecutorOptions{}).ExecuteBatch(context.Background(), requests)
	if len(results) != 2 || maximum.Load() != 1 {
		t.Fatalf("expected serial execution, max=%d results=%d", maximum.Load(), len(results))
	}
}

func TestExecuteBatchRunsReadOnlyToolsConcurrently(t *testing.T) {
	var active atomic.Int32
	var maximum atomic.Int32
	catalog, err := NewCatalog(concurrencyTestTool("reader", ConcurrencyPolicy{Mode: ConcurrencyReadOnly}, &active, &maximum))
	if err != nil {
		t.Fatalf("new catalog: %v", err)
	}
	requests := []ExecutionRequest{{Tool: "reader"}, {Tool: "reader"}}
	results := NewExecutor(catalog, ExecutorOptions{MaxConcurrency: 2}).ExecuteBatch(context.Background(), requests)
	if len(results) != 2 || maximum.Load() < 2 {
		t.Fatalf("expected read-only calls to overlap, max=%d results=%d", maximum.Load(), len(results))
	}
}

func TestExecuteBatchSerializesMatchingConcurrencyKeys(t *testing.T) {
	tracker := &keyConcurrencyTracker{active: make(map[string]int)}
	catalog, err := NewCatalog(Binding{
		Descriptor: Descriptor{
			Name: "keyed", Parameters: ObjectSchema(map[string]any{"resource_id": map[string]any{"type": "string"}}, []string{"resource_id"}),
			Concurrency: ConcurrencyPolicy{Mode: ConcurrencyKeyed, KeyArgument: "resource_id"},
		},
		Handler: tracker.handle,
	})
	if err != nil {
		t.Fatalf("new catalog: %v", err)
	}
	requests := []ExecutionRequest{
		{Tool: "keyed", Arguments: json.RawMessage(`{"resource_id":"a"}`)},
		{Tool: "keyed", Arguments: json.RawMessage(`{"resource_id":"a"}`)},
		{Tool: "keyed", Arguments: json.RawMessage(`{"resource_id":"b"}`)},
	}
	NewExecutor(catalog, ExecutorOptions{MaxConcurrency: 3}).ExecuteBatch(context.Background(), requests)
	if tracker.maxForKey("a") != 1 {
		t.Fatalf("matching keys overlapped: max=%d", tracker.maxForKey("a"))
	}
	if tracker.maxTotal < 2 {
		t.Fatalf("distinct keys did not overlap: max=%d", tracker.maxTotal)
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

type memoryEffectJournal struct {
	mu          sync.Mutex
	records     map[string]domain.ToolEffectRecord
	beginErr    error
	completeErr error
	markErr     error
	markCalls   int
}

func (j *memoryEffectJournal) BeginToolEffect(record domain.ToolEffectRecord) (domain.ToolEffectRecord, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.beginErr != nil {
		return domain.ToolEffectRecord{}, false, j.beginErr
	}
	if existing, ok := j.records[record.IdempotencyKey]; ok {
		return existing, false, nil
	}
	record.Status = domain.ToolEffectExecuting
	j.records[record.IdempotencyKey] = record
	return record, true, nil
}

func (j *memoryEffectJournal) CompleteToolEffect(key string, result []byte) (domain.ToolEffectRecord, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.completeErr != nil {
		return domain.ToolEffectRecord{}, j.completeErr
	}
	record := j.records[key]
	record.Status = domain.ToolEffectCommitted
	record.Result = append([]byte(nil), result...)
	j.records[key] = record
	return record, nil
}

func (j *memoryEffectJournal) MarkToolEffectNeedsReconciliation(key string, message string) (domain.ToolEffectRecord, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.markCalls++
	if j.markErr != nil {
		return domain.ToolEffectRecord{}, j.markErr
	}
	record := j.records[key]
	record.Status = domain.ToolEffectNeedsReconciliation
	record.Error = message
	j.records[key] = record
	return record, nil
}

func concurrencyTestTool(name string, policy ConcurrencyPolicy, active, maximum *atomic.Int32) Binding {
	return Binding{
		Descriptor: Descriptor{Name: name, Parameters: ObjectSchema(nil, nil), Concurrency: policy},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			current := active.Add(1)
			for {
				previous := maximum.Load()
				if current <= previous || maximum.CompareAndSwap(previous, current) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			active.Add(-1)
			return map[string]any{"ok": true}, nil
		},
	}
}

type keyConcurrencyTracker struct {
	mu       sync.Mutex
	active   map[string]int
	maximum  map[string]int
	total    int
	maxTotal int
}

func (t *keyConcurrencyTracker) handle(_ context.Context, args json.RawMessage) (any, error) {
	var input struct {
		ResourceID string `json:"resource_id"`
	}
	_ = json.Unmarshal(args, &input)
	t.mu.Lock()
	if t.maximum == nil {
		t.maximum = make(map[string]int)
	}
	t.active[input.ResourceID]++
	t.total++
	t.maximum[input.ResourceID] = max(t.maximum[input.ResourceID], t.active[input.ResourceID])
	t.maxTotal = max(t.maxTotal, t.total)
	t.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	t.mu.Lock()
	t.active[input.ResourceID]--
	t.total--
	t.mu.Unlock()
	return map[string]any{"resource_id": input.ResourceID}, nil
}

func (t *keyConcurrencyTracker) maxForKey(key string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.maximum[key]
}

func (t *recordingTracer) ToolStarted(_ context.Context, request ExecutionRequest) {
	t.started = request
}

func (t *recordingTracer) ToolFinished(_ context.Context, result ExecutionResult) {
	t.finished = result
}
