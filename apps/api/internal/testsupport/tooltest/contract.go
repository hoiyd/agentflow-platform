package tooltest

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/tools"
)

type InvalidCall struct {
	Name             string
	Arguments        json.RawMessage
	WantArgumentCode string
}

type BadResult struct {
	Name  string
	Value any
}

type BindingContract struct {
	Binding        tools.Binding
	ValidArguments json.RawMessage
	GoodResult     any
	InvalidCalls   []InvalidCall
	BadResults     []BadResult
	ValidateResult func(any) error
}

// RunBindingContract applies the same structural suite to every built-in
// Binding. Domain-specific behavior remains in the owner's tests; this suite
// proves that schema, canonical arguments, tracing, result sensors, and the
// side-effect replay boundary agree on one Tool contract.
func RunBindingContract(t *testing.T, spec BindingContract) {
	t.Helper()
	validateContractSpec(t, spec)

	var handlerCalls atomic.Int32
	binding := spec.Binding
	binding.Handler = func(context.Context, json.RawMessage) (any, error) {
		handlerCalls.Add(1)
		return spec.GoodResult, nil
	}
	catalog, err := tools.NewCatalog(binding)
	if err != nil {
		t.Fatalf("register binding: %v", err)
	}
	installed, ok := catalog.Installed(binding.Descriptor.Name)
	if !ok || installed.Descriptor.SchemaVersion != tools.ToolSchemaVersion || installed.Descriptor.DefinitionRevision == "" {
		t.Fatalf("binding has no compiled schema identity: %#v", installed.Descriptor)
	}
	assertModelSchemaMatchesContract(t, catalog, installed)

	tracer := &recordingTracer{}
	journal := newMemoryEffectJournal()
	executor := tools.NewExecutor(catalog, tools.ExecutorOptions{Tracer: tracer, EffectJournal: journal})
	request := tools.ExecutionRequest{
		CallID: "contract-valid", RunID: "run-contract", StageID: "stage-contract", TurnID: "turn-contract",
		Tool: binding.Descriptor.Name, Arguments: spec.ValidArguments,
	}
	result := executor.Execute(context.Background(), request)
	if result.Error != nil {
		t.Fatalf("valid call failed: %#v", result.Error)
	}
	if handlerCalls.Load() != 1 {
		t.Fatalf("valid call invoked handler %d times", handlerCalls.Load())
	}
	if result.ArgumentsHash == "" || result.DefinitionRevision != installed.Descriptor.DefinitionRevision {
		t.Fatalf("valid call lost canonical identity: %#v", result)
	}
	if err := spec.ValidateResult(result.Result); err != nil {
		t.Fatalf("good result failed deterministic sensor: %v", err)
	}
	for _, bad := range spec.BadResults {
		t.Run("reject result "+bad.Name, func(t *testing.T) {
			if err := spec.ValidateResult(bad.Value); err == nil {
				t.Fatal("known-bad result passed deterministic sensor")
			}
		})
	}

	for index, invalid := range spec.InvalidCalls {
		t.Run("reject arguments "+invalid.Name, func(t *testing.T) {
			before := handlerCalls.Load()
			result := executor.Execute(context.Background(), tools.ExecutionRequest{
				CallID: fmt.Sprintf("contract-invalid-%d", index), RunID: "run-contract", StageID: "stage-contract",
				Tool: binding.Descriptor.Name, Arguments: invalid.Arguments,
			})
			if result.Error == nil || result.Error.Code != tools.ErrorInvalidArgs || result.Error.Argument == nil {
				t.Fatalf("expected typed invalid arguments, got %#v", result.Error)
			}
			if invalid.WantArgumentCode != "" && result.Error.Argument.Code != invalid.WantArgumentCode {
				t.Fatalf("argument code = %q, want %q", result.Error.Argument.Code, invalid.WantArgumentCode)
			}
			if handlerCalls.Load() != before {
				t.Fatal("invalid arguments reached handler")
			}
		})
	}

	if binding.Descriptor.SideEffect.Mode == tools.SideEffectExternal {
		replayed := executor.Execute(context.Background(), request)
		if replayed.Error != nil || !replayed.Replayed || handlerCalls.Load() != 1 {
			t.Fatalf("side-effect replay violated contract: result=%#v calls=%d", replayed, handlerCalls.Load())
		}
	}
	if tracer.starts.Load() == 0 || tracer.starts.Load() != tracer.finishes.Load() {
		t.Fatalf("trace callbacks are not paired: starts=%d finishes=%d", tracer.starts.Load(), tracer.finishes.Load())
	}
	if tracer.validHash == "" || tracer.validRevision != installed.Descriptor.DefinitionRevision {
		t.Fatalf("trace lost contract identity: hash=%q revision=%q", tracer.validHash, tracer.validRevision)
	}
}

func validateContractSpec(t *testing.T, spec BindingContract) {
	t.Helper()
	if spec.Binding.Descriptor.Name == "" || spec.Binding.Descriptor.Parameters == nil || spec.Binding.Handler == nil {
		t.Fatal("binding contract requires a complete Binding")
	}
	if len(spec.ValidArguments) == 0 || len(spec.InvalidCalls) == 0 {
		t.Fatal("binding contract requires valid and invalid argument cases")
	}
	if spec.ValidateResult == nil || len(spec.BadResults) == 0 {
		t.Fatal("binding contract requires a deterministic result sensor and known-bad result")
	}
}

func assertModelSchemaMatchesContract(t *testing.T, catalog *tools.Catalog, binding tools.Binding) {
	t.Helper()
	definitions := catalog.Definitions()
	if len(definitions) != 1 {
		t.Fatalf("model definition count = %d, want 1", len(definitions))
	}
	function, ok := definitions[0]["function"].(map[string]any)
	if !ok {
		t.Fatalf("invalid model definition: %#v", definitions[0])
	}
	modelSchema, _ := json.Marshal(function["parameters"])
	contractSchema, _ := json.Marshal(binding.Descriptor.Parameters)
	if string(modelSchema) != string(contractSchema) {
		t.Fatalf("model schema differs from compiled contract: %s != %s", modelSchema, contractSchema)
	}
}

type recordingTracer struct {
	starts        atomic.Int32
	finishes      atomic.Int32
	validHash     string
	validRevision string
}

func (t *recordingTracer) ToolStarted(_ context.Context, request tools.ExecutionRequest) {
	t.starts.Add(1)
	if request.CallID == "contract-valid" {
		t.validHash = request.ArgumentsHash
		t.validRevision = request.DefinitionRevision
	}
}

func (t *recordingTracer) ToolFinished(context.Context, tools.ExecutionResult) {
	t.finishes.Add(1)
}

type memoryEffectJournal struct {
	records map[string]domain.ToolEffectRecord
}

func newMemoryEffectJournal() *memoryEffectJournal {
	return &memoryEffectJournal{records: make(map[string]domain.ToolEffectRecord)}
}

func (j *memoryEffectJournal) BeginToolEffect(record domain.ToolEffectRecord) (domain.ToolEffectRecord, bool, error) {
	if existing, ok := j.records[record.IdempotencyKey]; ok {
		return existing, false, nil
	}
	record.Status = domain.ToolEffectExecuting
	j.records[record.IdempotencyKey] = record
	return record, true, nil
}

func (j *memoryEffectJournal) CompleteToolEffect(key string, result []byte) (domain.ToolEffectRecord, error) {
	record := j.records[key]
	record.Status = domain.ToolEffectCommitted
	record.Result = append([]byte(nil), result...)
	j.records[key] = record
	return record, nil
}

func (j *memoryEffectJournal) MarkToolEffectNeedsReconciliation(key, message string) (domain.ToolEffectRecord, error) {
	record := j.records[key]
	record.Status = domain.ToolEffectNeedsReconciliation
	record.Error = message
	j.records[key] = record
	return record, nil
}
