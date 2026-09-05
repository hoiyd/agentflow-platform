package toolreconciliation

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/toolpolicy"
	"agentflow-platform/apps/api/internal/tools"
)

func TestConfirmCommittedIsAuditedAndIdempotent(t *testing.T) {
	fileStore, run, catalog, effect := reconciliationFixture(t, tools.SideEffectReconciliation{})
	command := ToolEffectReconciliationCommand{
		CommandID: "command-1", Action: domain.ToolEffectConfirmCommitted,
		ExpectedVersion: effect.Version, Actor: "operator@example.com", Reason: "verified in provider audit log",
		Result: json.RawMessage(`{"remote_id":"record-1"}`),
	}
	first, err := ReconcileToolEffect(context.Background(), catalog, fileStore, run, effect.IdempotencyKey, command)
	if err != nil || !first.Applied || first.Outcome != "completed" || first.Effect.Status != domain.ToolEffectCommitted || first.Effect.Version != effect.Version+1 {
		t.Fatalf("first reconciliation: outcome=%#v err=%v", first, err)
	}
	second, err := ReconcileToolEffect(context.Background(), catalog, fileStore, run, effect.IdempotencyKey, command)
	if err != nil || second.Applied || second.Effect.Status != domain.ToolEffectCommitted {
		t.Fatalf("duplicate reconciliation: outcome=%#v err=%v", second, err)
	}
	events, _ := fileStore.ListRunEvents(run.ID)
	if len(events) != 1 || events[0].Type != domain.EventToolEffectReconciled || events[0].Payload["actor"] != command.Actor || events[0].Payload["result_version"] != effect.Version+1 {
		t.Fatalf("unexpected audit events: %#v", events)
	}
	records, _ := fileStore.ListToolEffects(run.ID)
	var replay tools.ExecutionResult
	if err := json.Unmarshal(records[0].Result, &replay); err != nil || replay.Tool != effect.ToolName || replay.Result.(map[string]any)["remote_id"] != "record-1" {
		t.Fatalf("invalid replay envelope: %#v err=%v", replay, err)
	}
}

func TestRetryAndCompensationRequireExplicitBindingCapabilities(t *testing.T) {
	fileStore, run, catalog, effect := reconciliationFixture(t, tools.SideEffectReconciliation{})
	command := ToolEffectReconciliationCommand{
		CommandID: "retry-denied", Action: domain.ToolEffectRetrySameKey,
		ExpectedVersion: effect.Version, Actor: "operator", Reason: "provider confirmed safe retry",
	}
	if _, err := ReconcileToolEffect(context.Background(), catalog, fileStore, run, effect.IdempotencyKey, command); reconciliationCode(err) != ReconciliationUnavailable {
		t.Fatalf("expected unavailable retry, got %v", err)
	}
	records, _ := fileStore.ListToolEffects(run.ID)
	if records[0].Version != effect.Version || records[0].Status != domain.ToolEffectNeedsReconciliation {
		t.Fatalf("denied command changed effect: %#v", records[0])
	}

	retryCalls := 0
	fileStore, run, catalog, effect = reconciliationFixture(t, tools.SideEffectReconciliation{
		RetryWithSameKey: func(_ context.Context, recovery tools.EffectReconciliationContext) (any, error) {
			retryCalls++
			if recovery.IdempotencyKey != effect.IdempotencyKey {
				t.Fatalf("retry changed idempotency key: %#v", recovery)
			}
			return map[string]any{"retried": true}, nil
		},
	})
	command.CommandID, command.ExpectedVersion = "retry-allowed", effect.Version
	first, err := ReconcileToolEffect(context.Background(), catalog, fileStore, run, effect.IdempotencyKey, command)
	if err != nil || first.Effect.Status != domain.ToolEffectCommitted || retryCalls != 1 {
		t.Fatalf("retry outcome=%#v calls=%d err=%v", first, retryCalls, err)
	}
	if _, err := ReconcileToolEffect(context.Background(), catalog, fileStore, run, effect.IdempotencyKey, command); err != nil || retryCalls != 1 {
		t.Fatalf("duplicate retry called provider again: calls=%d err=%v", retryCalls, err)
	}

	compensated := false
	fileStore, run, catalog, effect = reconciliationFixture(t, tools.SideEffectReconciliation{
		Compensate: func(_ context.Context, recovery tools.EffectReconciliationContext) error {
			compensated = recovery.CompensationKey != ""
			return nil
		},
	})
	command = ToolEffectReconciliationCommand{
		CommandID: "compensate-1", Action: domain.ToolEffectCompensate,
		ExpectedVersion: effect.Version, Actor: "operator", Reason: "rollback approved",
	}
	outcome, err := ReconcileToolEffect(context.Background(), catalog, fileStore, run, effect.IdempotencyKey, command)
	if err != nil || !compensated || outcome.Effect.Status != domain.ToolEffectCompensated {
		t.Fatalf("compensation outcome=%#v called=%v err=%v", outcome, compensated, err)
	}
}

func TestFailedRetryRemainsQueryableAndDoesNotRepeatCommand(t *testing.T) {
	calls := 0
	fileStore, run, catalog, effect := reconciliationFixture(t, tools.SideEffectReconciliation{
		RetryWithSameKey: func(context.Context, tools.EffectReconciliationContext) (any, error) {
			calls++
			return nil, errors.New("provider still unavailable")
		},
	})
	command := ToolEffectReconciliationCommand{
		CommandID: "retry-failed", Action: domain.ToolEffectRetrySameKey,
		ExpectedVersion: effect.Version, Actor: "operator", Reason: "retry after outage",
	}
	first, err := ReconcileToolEffect(context.Background(), catalog, fileStore, run, effect.IdempotencyKey, command)
	if err != nil || first.Outcome != "failed" || first.Effect.Status != domain.ToolEffectNeedsReconciliation || first.Effect.Version != effect.Version+1 || calls != 1 {
		t.Fatalf("failed retry: outcome=%#v calls=%d err=%v", first, calls, err)
	}
	second, err := ReconcileToolEffect(context.Background(), catalog, fileStore, run, effect.IdempotencyKey, command)
	if err != nil || second.Applied || calls != 1 {
		t.Fatalf("duplicate failed retry: outcome=%#v calls=%d err=%v", second, calls, err)
	}
	events, _ := fileStore.ListRunEvents(run.ID)
	if len(events) != 1 || events[0].Type != domain.EventToolEffectReconciliationFailed || events[0].Payload["error"] != "provider still unavailable" {
		t.Fatalf("unexpected failure audit: %#v", events)
	}
}

func TestReconciliationRejectsInvalidStateVersionAndDefinition(t *testing.T) {
	fileStore, run, catalog, effect := reconciliationFixture(t, tools.SideEffectReconciliation{
		RetryWithSameKey: func(context.Context, tools.EffectReconciliationContext) (any, error) { return nil, nil },
	})
	base := ToolEffectReconciliationCommand{CommandID: "command", Action: domain.ToolEffectConfirmFailed, ExpectedVersion: effect.Version, Actor: "operator", Reason: "not applied"}
	wrongVersion := base
	wrongVersion.ExpectedVersion++
	if _, err := ReconcileToolEffect(context.Background(), catalog, fileStore, run, effect.IdempotencyKey, wrongVersion); reconciliationCode(err) != ReconciliationConflict {
		t.Fatalf("expected version conflict, got %v", err)
	}
	if _, err := fileStore.MarkToolEffectNeedsReconciliation(effect.IdempotencyKey, "still uncertain"); err != nil {
		t.Fatal(err)
	}
	command := base
	command.CommandID, command.Action, command.ExpectedVersion = "retry-mismatch", domain.ToolEffectRetrySameKey, effect.Version+1
	// The persisted revision remains stable; use a second catalog to emulate Binding drift.
	drifted, err := tools.NewCatalog(externalBinding(tools.SideEffectReconciliation{
		RetryWithSameKey: func(context.Context, tools.EffectReconciliationContext) (any, error) { return nil, nil },
	}, "changed description"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcileToolEffect(context.Background(), drifted, fileStore, run, effect.IdempotencyKey, command); reconciliationCode(err) != ReconciliationMismatch {
		t.Fatalf("expected definition mismatch, got %v", err)
	}
	base.CommandID = "invalid"
	base.Action = "unknown"
	if _, err := ReconcileToolEffect(context.Background(), catalog, fileStore, run, effect.IdempotencyKey, base); reconciliationCode(err) != ReconciliationInvalid {
		t.Fatalf("expected invalid action, got %v", err)
	}
}

func TestReconciliationValidationAndHelpers(t *testing.T) {
	valid := ToolEffectReconciliationCommand{
		CommandID: "command", Action: domain.ToolEffectConfirmFailed, ExpectedVersion: 1,
		Actor: "operator", Reason: "checked",
	}
	for _, test := range []struct {
		name   string
		key    string
		mutate func(*ToolEffectReconciliationCommand)
	}{
		{name: "missing identity", key: "", mutate: func(*ToolEffectReconciliationCommand) {}},
		{name: "large metadata", key: "effect", mutate: func(item *ToolEffectReconciliationCommand) { item.Actor = string(make([]byte, 129)) }},
		{name: "missing result", key: "effect", mutate: func(item *ToolEffectReconciliationCommand) { item.Action = domain.ToolEffectConfirmCommitted }},
		{name: "unexpected result", key: "effect", mutate: func(item *ToolEffectReconciliationCommand) { item.Result = json.RawMessage(`{}`) }},
		{name: "unknown action", key: "effect", mutate: func(item *ToolEffectReconciliationCommand) { item.Action = "unknown" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := valid
			test.mutate(&command)
			if reconciliationCode(validateReconciliationCommand(test.key, command)) != ReconciliationInvalid {
				t.Fatal("expected invalid command")
			}
		})
	}

	typed := &ReconciliationError{Code: ReconciliationNotFound, Message: "missing"}
	if typed.Error() != "missing" || typed.FailureInfo().Category == "" {
		t.Fatalf("invalid reconciliation error: %#v", typed)
	}
	if ValidToolEffectStatus("unknown") || !ValidToolEffectStatus(domain.ToolEffectFailed) {
		t.Fatal("unexpected status validation")
	}
	if actions := NewToolEffectViews(nil, []domain.ToolEffectRecord{{Status: domain.ToolEffectCommitted}})[0].AvailableActions; len(actions) != 0 {
		t.Fatalf("terminal effect actions: %v", actions)
	}
	if _, err := reconciliationBinding(nil, domain.ToolEffectRecord{}); reconciliationCode(err) != ReconciliationUnavailable {
		t.Fatalf("nil catalog error: %v", err)
	}
	if _, _, err := executeReconciliationAction(context.Background(), nil, domain.ToolEffectRecord{}, ToolEffectReconciliationCommand{Action: "unknown"}); reconciliationCode(err) != ReconciliationInvalid {
		t.Fatalf("unknown action error: %v", err)
	}
	if _, _, err := executeReconciliationAction(context.Background(), nil, domain.ToolEffectRecord{}, ToolEffectReconciliationCommand{Action: domain.ToolEffectConfirmCommitted, Result: json.RawMessage(`{`)}); reconciliationCode(err) != ReconciliationInvalid {
		t.Fatalf("invalid result error: %v", err)
	}
	if _, err := encodeReconciledResult(domain.ToolEffectRecord{}, make(chan int)); err == nil {
		t.Fatal("unsupported result should fail encoding")
	}
	if _, err := callEffectRetry(context.Background(), func(context.Context, tools.EffectReconciliationContext) (any, error) { panic("retry") }, tools.EffectReconciliationContext{}); err == nil {
		t.Fatal("retry panic should become an error")
	}
	if err := callEffectCompensation(context.Background(), func(context.Context, tools.EffectReconciliationContext) error { panic("compensate") }, tools.EffectReconciliationContext{}); err == nil {
		t.Fatal("compensation panic should become an error")
	}
	if got := boundedReconciliationText("abc\xe4\xb8", 4); got != "abc" {
		t.Fatalf("invalid UTF-8 boundary: %q", got)
	}
}

func TestReconciliationRejectsTerminalStateAndUnavailableCompensation(t *testing.T) {
	fileStore, run, catalog, effect := reconciliationFixture(t, tools.SideEffectReconciliation{})
	command := ToolEffectReconciliationCommand{
		CommandID: "compensate-denied", Action: domain.ToolEffectCompensate,
		ExpectedVersion: effect.Version, Actor: "operator", Reason: "rollback requested",
	}
	if _, err := ReconcileToolEffect(context.Background(), catalog, fileStore, run, effect.IdempotencyKey, command); reconciliationCode(err) != ReconciliationUnavailable {
		t.Fatalf("expected unavailable compensation, got %v", err)
	}
	command.CommandID, command.Action = "confirm-failed", domain.ToolEffectConfirmFailed
	if _, err := ReconcileToolEffect(context.Background(), catalog, fileStore, run, effect.IdempotencyKey, command); err != nil {
		t.Fatal(err)
	}
	command.CommandID, command.ExpectedVersion = "terminal-command", effect.Version+1
	if _, err := ReconcileToolEffect(context.Background(), catalog, fileStore, run, effect.IdempotencyKey, command); reconciliationCode(err) != ReconciliationConflict {
		t.Fatalf("expected terminal state conflict, got %v", err)
	}

	_, _, compensationCatalog, uncertain := reconciliationFixture(t, tools.SideEffectReconciliation{
		Compensate: func(context.Context, tools.EffectReconciliationContext) error { return nil },
	})
	views := NewToolEffectViews(compensationCatalog, []domain.ToolEffectRecord{uncertain})
	if len(views[0].AvailableActions) != 3 || views[0].AvailableActions[2] != domain.ToolEffectCompensate {
		t.Fatalf("compensation action missing: %#v", views)
	}
}

func TestReconciliationPropagatesStoreFailures(t *testing.T) {
	want := errors.New("store failed")
	run := domain.Run{ID: "run-1"}
	command := ToolEffectReconciliationCommand{
		CommandID: "command", Action: domain.ToolEffectConfirmFailed, ExpectedVersion: 1,
		Actor: "operator", Reason: "checked",
	}
	stub := &reconciliationStoreStub{listErr: want}
	if _, err := ReconcileToolEffect(context.Background(), nil, stub, run, "effect", command); !errors.Is(err, want) {
		t.Fatalf("list error: %v", err)
	}
	stub = &reconciliationStoreStub{effects: []domain.ToolEffectRecord{{
		IdempotencyKey: "effect", Version: 1, RunID: run.ID, StageID: "stage-1",
		Status: domain.ToolEffectNeedsReconciliation,
	}}, eventErr: want}
	if _, err := ReconcileToolEffect(context.Background(), nil, stub, run, "effect", command); !errors.Is(err, want) {
		t.Fatalf("event error: %v", err)
	}
	stub.eventErr, stub.commitErr = nil, want
	stub.events = []domain.RunEvent{{Type: domain.EventRunStarted}}
	if _, err := ReconcileToolEffect(context.Background(), nil, stub, run, "effect", command); !errors.Is(err, want) {
		t.Fatalf("commit error: %v", err)
	}
}

type reconciliationStoreStub struct {
	effects   []domain.ToolEffectRecord
	events    []domain.RunEvent
	listErr   error
	eventErr  error
	commitErr error
}

func (s *reconciliationStoreStub) ListToolEffects(string) ([]domain.ToolEffectRecord, error) {
	return s.effects, s.listErr
}

func (s *reconciliationStoreStub) ListRunEvents(string) ([]domain.RunEvent, error) {
	return s.events, s.eventErr
}

func (s *reconciliationStoreStub) CommitToolEffectReconciliation(mutation domain.ToolEffectReconciliation) (domain.ToolEffectRecord, domain.RunEvent, bool, error) {
	return s.effects[0], mutation.Event, s.commitErr == nil, s.commitErr
}

func reconciliationFixture(t *testing.T, recovery tools.SideEffectReconciliation) (*store.FileStore, domain.Run, *tools.Catalog, domain.ToolEffectRecord) {
	t.Helper()
	fileStore, err := store.NewFileStore(t.TempDir() + "/agentflow.json")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := fileStore.CreateConversation("reconciliation")
	if err != nil {
		t.Fatal(err)
	}
	run, err := fileStore.CreateRunWithContract("agent_planner", conversation.ID, domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := tools.NewCatalog(externalBinding(recovery, "writes a record"))
	if err != nil {
		t.Fatal(err)
	}
	binding, _ := catalog.Installed("write_record")
	effect, execute, err := fileStore.BeginToolEffect(domain.ToolEffectRecord{
		IdempotencyKey: "effect-1", RunID: run.ID, StageID: "stage-1", TurnID: "turn-1",
		ToolCallID: "call-1", ToolName: binding.Descriptor.Name,
		DefinitionRevision: binding.Descriptor.DefinitionRevision, RequestHash: "request-hash",
	})
	if err != nil || !execute {
		t.Fatalf("begin effect: effect=%#v execute=%v err=%v", effect, execute, err)
	}
	effect, err = fileStore.MarkToolEffectNeedsReconciliation(effect.IdempotencyKey, "timeout")
	if err != nil {
		t.Fatal(err)
	}
	return fileStore, run, catalog, effect
}

func externalBinding(recovery tools.SideEffectReconciliation, description string) tools.Binding {
	capability := toolpolicy.NormalizeCapability(toolpolicy.Capability{
		Scope:      toolpolicy.Scope{Resources: []toolpolicy.ResourceScope{{Kind: toolpolicy.ResourceExternal, Name: "records", Access: toolpolicy.AccessWrite}}},
		SideEffect: toolpolicy.SideEffectExternalWrite, Reversibility: toolpolicy.Compensatable,
		Visibility: toolpolicy.VisibilityOperator, Audit: toolpolicy.AuditFull,
	})
	return tools.Binding{
		Descriptor: tools.Descriptor{
			Name: "write_record", Description: description, Parameters: tools.ObjectSchema(nil, nil),
			SideEffect: tools.SideEffectPolicy{
				Mode: tools.SideEffectExternal, RetryWithSameKey: recovery.RetryWithSameKey != nil,
				Compensate: recovery.Compensate != nil,
			}, Security: capability,
		},
		Handler:        func(context.Context, json.RawMessage) (any, error) { return nil, nil },
		Reconciliation: recovery,
	}
}

func reconciliationCode(err error) string {
	var typed *ReconciliationError
	if errors.As(err, &typed) {
		return typed.Code
	}
	return ""
}
