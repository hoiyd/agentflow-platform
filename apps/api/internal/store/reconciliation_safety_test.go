package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/toolpolicy"
	"agentflow-platform/apps/api/internal/toolreconciliation"
	"agentflow-platform/apps/api/internal/tools"
)

func safetyFixture(t *testing.T, backend string, callbacks tools.SideEffectReconciliation) (Store, func() Store, domain.Run, *tools.Catalog, domain.ToolEffectRecord) {
	t.Helper()
	binding := tools.Binding{
		Descriptor: tools.Descriptor{Name: "write_record", Description: "writes a record", Parameters: tools.ObjectSchema(nil, nil),
			SideEffect: tools.SideEffectPolicy{Mode: tools.SideEffectExternal, RetryWithSameKey: callbacks.RetryWithSameKey != nil, Compensate: callbacks.Compensate != nil},
			Security:   toolpolicy.NormalizeCapability(toolpolicy.Capability{SideEffect: toolpolicy.SideEffectExternalWrite, Reversibility: toolpolicy.Compensatable}),
		},
		Handler:        func(context.Context, json.RawMessage) (any, error) { return nil, nil },
		Reconciliation: callbacks,
	}
	catalog, err := tools.NewCatalogWithPolicy(toolpolicy.Policy{Version: toolpolicy.CurrentVersion, DefaultAction: toolpolicy.ActionDeny,
		Rules: []toolpolicy.Rule{{ID: "recovery", Tool: binding.Descriptor.Name, Action: toolpolicy.ActionAllowAndLog, Capability: binding.Descriptor.Security}},
	}, binding)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "effects.json")
	open := func() Store {
		t.Helper()
		if backend == "postgres" {
			url := os.Getenv("TEST_DATABASE_URL")
			if url == "" {
				t.Skip("TEST_DATABASE_URL is not set")
			}
			pg, err := NewPostgresStore(url)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = pg.Close() })
			return pg
		}
		file, err := NewFileStore(path)
		if err != nil {
			t.Fatal(err)
		}
		return file
	}
	target := open()
	conversation, err := target.CreateConversation("reconciliation safety")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.DeleteConversation(conversation.ID) })
	run, err := target.CreateRunWithContract("agent_planner", conversation.ID, domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	binding, _ = catalog.Resolve("write_record")
	effect, _, err := target.BeginToolEffect(domain.ToolEffectRecord{
		IdempotencyKey: "effect-" + run.ID, RunID: run.ID, StageID: "stage-1", TurnID: "turn-1",
		ToolCallID: "call-1", ToolName: binding.Descriptor.Name, DefinitionRevision: binding.Descriptor.DefinitionRevision, RequestHash: "request",
	})
	if err != nil {
		t.Fatal(err)
	}
	effect, err = target.MarkToolEffectNeedsReconciliation(effect.IdempotencyKey, "uncertain")
	if err != nil {
		t.Fatal(err)
	}
	return target, open, run, catalog, effect
}

func retryCommand(effect domain.ToolEffectRecord) toolreconciliation.ToolEffectReconciliationCommand {
	return toolreconciliation.ToolEffectReconciliationCommand{CommandID: "retry-1", Action: domain.ToolEffectRetrySameKey,
		ExpectedVersion: effect.Version, Actor: "operator", Reason: "checked provider"}
}

func TestCallbackClaimSerializesConcurrentCommands(t *testing.T) {
	for _, backend := range []string{"file", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			var calls atomic.Int32
			target, _, run, catalog, effect := safetyFixture(t, backend, tools.SideEffectReconciliation{
				RetryWithSameKey: func(context.Context, tools.EffectReconciliationContext) (any, error) {
					if calls.Add(1) == 1 {
						close(entered)
					}
					<-release
					return map[string]any{"ok": true}, nil
				},
			})
			command := retryCommand(effect)
			done := make(chan error, 1)
			go func() {
				result, err := toolreconciliation.ReconcileToolEffect(context.Background(), catalog, target, run, effect.IdempotencyKey, command)
				if err == nil && (result.Effect.Status != domain.ToolEffectCommitted || result.Effect.Version != effect.Version+2) {
					err = errors.New("incorrect settlement")
				}
				done <- err
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("callback did not start")
			}
			var contenders sync.WaitGroup
			for i := 0; i < 12; i++ {
				contenders.Add(1)
				go func(index int) {
					defer contenders.Done()
					other := command
					if index%3 == 1 {
						other.CommandID = "competing"
					}
					if index%3 == 2 {
						other.Reason = "changed payload"
					}
					result, err := toolreconciliation.ReconcileToolEffect(context.Background(), catalog, target, run, effect.IdempotencyKey, other)
					if index%3 == 0 {
						if err != nil || result.Applied || result.Outcome != "pending" || result.Effect.Status != domain.ToolEffectReconciling || len(result.Effect.AvailableActions) != 2 {
							t.Errorf("duplicate: %#v %v", result, err)
						}
					} else if reconciliationCode(err) != toolreconciliation.ReconciliationConflict {
						t.Errorf("expected conflict: %v", err)
					}
				}(i)
			}
			contenders.Wait()
			if calls.Load() != 1 {
				t.Fatalf("callbacks=%d", calls.Load())
			}
			if _, err := target.MarkToolEffectNeedsReconciliation(effect.IdempotencyKey, "late failure"); err == nil {
				t.Fatal("claim reopened")
			}
			if _, err := target.CompleteToolEffect(effect.IdempotencyKey, []byte(`{}`)); err == nil {
				t.Fatal("late execution overwrote claim")
			}
			releaseOnce.Do(func() { close(release) })
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			duplicate, err := toolreconciliation.ReconcileToolEffect(context.Background(), catalog, target, run, effect.IdempotencyKey, command)
			if err != nil || duplicate.Applied || duplicate.Outcome != "completed" || calls.Load() != 1 {
				t.Fatalf("settled duplicate: %#v %v", duplicate, err)
			}
		})
	}
}

type failingSettlementStore struct {
	Store
	failClaim bool
}

func (s failingSettlementStore) CommitToolEffectReconciliation(m domain.ToolEffectReconciliation) (domain.ToolEffectRecord, domain.RunEvent, bool, error) {
	if (m.Event.Type == domain.EventToolEffectReconciliationStarted) == s.failClaim {
		return domain.ToolEffectRecord{}, domain.RunEvent{}, false, errors.New("injected persistence failure")
	}
	return s.Store.CommitToolEffectReconciliation(m)
}

func TestClaimAndSettlementFailureWindows(t *testing.T) {
	for _, backend := range []string{"file", "postgres"} {
		for _, failClaim := range []bool{true, false} {
			name := backend + "/settlement"
			if failClaim {
				name = backend + "/claim"
			}
			t.Run(name, func(t *testing.T) {
				var calls atomic.Int32
				target, reopen, run, catalog, effect := safetyFixture(t, backend, tools.SideEffectReconciliation{
					RetryWithSameKey: func(context.Context, tools.EffectReconciliationContext) (any, error) { calls.Add(1); return true, nil },
				})
				command := retryCommand(effect)
				_, err := toolreconciliation.ReconcileToolEffect(context.Background(), catalog, failingSettlementStore{target, failClaim}, run, effect.IdempotencyKey, command)
				if err == nil {
					t.Fatal("expected persistence failure")
				}
				restarted := reopen()
				effects, err := restarted.ListToolEffects(run.ID)
				if err != nil || len(effects) != 1 {
					t.Fatalf("read after restart: %v", err)
				}
				if failClaim {
					if calls.Load() != 0 || effects[0].Version != effect.Version || effects[0].Status != domain.ToolEffectNeedsReconciliation {
						t.Fatal("failed claim invoked callback or changed effect")
					}
					return
				}
				if calls.Load() != 1 || effects[0].Status != domain.ToolEffectReconciling {
					t.Fatal("lost durable claim")
				}
				duplicate, err := toolreconciliation.ReconcileToolEffect(context.Background(), catalog, restarted, run, effect.IdempotencyKey, command)
				if err != nil || duplicate.Outcome != "pending" || calls.Load() != 1 {
					t.Fatalf("replayed unknown callback: %#v %v", duplicate, err)
				}
				confirm := command
				confirm.CommandID, confirm.Action, confirm.ExpectedVersion = "manual", domain.ToolEffectConfirmFailed, effects[0].Version
				result, err := toolreconciliation.ReconcileToolEffect(context.Background(), catalog, restarted, run, effect.IdempotencyKey, confirm)
				if err != nil || result.Effect.Status != domain.ToolEffectFailed {
					t.Fatalf("manual recovery: %#v %v", result, err)
				}
				if _, err := restarted.MarkToolEffectNeedsReconciliation(effect.IdempotencyKey, "late failure"); err == nil {
					t.Fatal("failed terminal reopened")
				}
			})
		}
	}
}

func TestCanceledCallbackRemainsClaimedAndCannotOverwriteManualResolution(t *testing.T) {
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer func() { close(release); <-finished }()
	target, run, catalog, effect := fileReconciliationFixture(t, tools.SideEffectReconciliation{
		RetryWithSameKey: func(context.Context, tools.EffectReconciliationContext) (any, error) {
			close(entered)
			<-release
			defer close(finished)
			return true, nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := retryCommand(effect)
	done := make(chan toolreconciliation.ToolEffectReconciliationOutcome, 1)
	go func() {
		result, err := toolreconciliation.ReconcileToolEffect(ctx, catalog, target, run, effect.IdempotencyKey, command)
		if err != nil {
			t.Error(err)
		}
		done <- result
	}()
	<-entered
	cancel()
	var result toolreconciliation.ToolEffectReconciliationOutcome
	select {
	case result = <-done:
	case <-time.After(time.Second):
		t.Fatal("ignored cancellation blocked HTTP caller")
	}
	if result.Effect.Status != domain.ToolEffectReconciling || len(result.Effect.AvailableActions) != 2 {
		t.Fatalf("unsafe cancellation: %#v", result)
	}
	other := command
	other.CommandID, other.ExpectedVersion = "second", result.Effect.Version
	if _, err := toolreconciliation.ReconcileToolEffect(context.Background(), catalog, target, run, effect.IdempotencyKey, other); reconciliationCode(err) != toolreconciliation.ReconciliationConflict {
		t.Fatalf("callback reopened: %v", err)
	}
	other.Action = domain.ToolEffectConfirmCommitted
	other.Result = json.RawMessage(`"` + strings.Repeat("x", tools.DefaultMaxResultBytes-2) + `"`)
	invalid, err := toolreconciliation.ReconcileToolEffect(context.Background(), catalog, target, run, effect.IdempotencyKey, other)
	if err != nil || invalid.Outcome != "failed" || invalid.Effect.Status != domain.ToolEffectReconciling {
		t.Fatalf("invalid confirmation released callback claim: %#v %v", invalid, err)
	}
	other.CommandID, other.ExpectedVersion = "valid-confirmation", invalid.Effect.Version
	other.Result = json.RawMessage(`{"checked":true}`)
	confirmed, err := toolreconciliation.ReconcileToolEffect(context.Background(), catalog, target, run, effect.IdempotencyKey, other)
	if err != nil || confirmed.Effect.Status != domain.ToolEffectCommitted {
		t.Fatalf("manual confirmation: %#v %v", confirmed, err)
	}
}

func TestReconciliationRedactsAllPersistedSurfaces(t *testing.T) {
	for _, action := range []domain.ToolEffectReconciliationAction{domain.ToolEffectConfirmCommitted, domain.ToolEffectConfirmFailed, domain.ToolEffectRetrySameKey} {
		t.Run(string(action), func(t *testing.T) {
			target, run, catalog, effect := fileReconciliationFixture(t, tools.SideEffectReconciliation{RetryWithSameKey: func(context.Context, tools.EffectReconciliationContext) (any, error) {
				return nil, errors.New("Bearer callback-secret")
			}})
			command := retryCommand(effect)
			command.Action, command.Actor, command.Reason = action, "token=actor-secret", "api_key=reason-secret"
			if action == domain.ToolEffectConfirmCommitted {
				command.Result = json.RawMessage(`{"authorization":"result-secret","nested":["Bearer nested-secret"]}`)
			}
			if _, err := toolreconciliation.ReconcileToolEffect(context.Background(), catalog, target, run, effect.IdempotencyKey, command); err != nil {
				t.Fatal(err)
			}
			effects, _ := target.ListToolEffects(run.ID)
			events, _ := target.ListRunEvents(run.ID)
			encoded, _ := json.Marshal(events)
			all := string(encoded) + effects[0].Error + string(effects[0].Result)
			for _, secret := range []string{"actor-secret", "reason-secret", "result-secret", "nested-secret", "callback-secret"} {
				if strings.Contains(all, secret) {
					t.Fatalf("persisted secret %s", secret)
				}
			}
		})
	}
}

func TestReconciliationPolicyAndEnablementFailClosed(t *testing.T) {
	for _, mode := range []string{"denied", "disabled", "credential", "approval"} {
		t.Run(mode, func(t *testing.T) {
			target, run, catalog, effect := fileReconciliationFixture(t, tools.SideEffectReconciliation{RetryWithSameKey: func(context.Context, tools.EffectReconciliationContext) (any, error) {
				t.Error("unauthorized callback")
				return nil, nil
			}})
			binding, _ := catalog.Resolve(effect.ToolName)
			policy := catalog.SecurityPolicy()
			switch mode {
			case "denied":
				policy.Rules[0].Action = toolpolicy.ActionDeny
			case "credential":
				binding.Descriptor.DefinitionRevision = ""
				binding.Descriptor.Security.Scope.Credentials = []string{"payments"}
				policy.Rules[0].Capability = binding.Descriptor.Security
			case "approval":
				policy.Rules[0].Action = toolpolicy.ActionAsk
			}
			var err error
			catalog, err = tools.NewCatalogWithPolicy(policy, binding)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "credential" {
				current, _ := catalog.Resolve(effect.ToolName)
				effect.DefinitionRevision = current.Descriptor.DefinitionRevision
				targetAdapter := &reconciliationRecordView{Store: target, effects: []domain.ToolEffectRecord{effect}}
				if _, err := toolreconciliation.ReconcileToolEffect(context.Background(), catalog, targetAdapter, run, effect.IdempotencyKey, retryCommand(effect)); reconciliationCode(err) != toolreconciliation.ReconciliationUnavailable {
					t.Fatalf("credentials: %v", err)
				}
				return
			}
			if mode == "disabled" {
				_ = catalog.SetEnabled(effect.ToolName, false)
			}
			if _, err := toolreconciliation.ReconcileToolEffect(context.Background(), catalog, target, run, effect.IdempotencyKey, retryCommand(effect)); reconciliationCode(err) != toolreconciliation.ReconciliationUnavailable {
				t.Fatalf("policy: %v", err)
			}
			events, _ := target.ListRunEvents(run.ID)
			if len(events) != 0 {
				t.Fatal("denied callback claimed effect")
			}
		})
	}
}

func fileReconciliationFixture(t *testing.T, callbacks tools.SideEffectReconciliation) (*FileStore, domain.Run, *tools.Catalog, domain.ToolEffectRecord) {
	target, _, run, catalog, effect := safetyFixture(t, "file", callbacks)
	return target.(*FileStore), run, catalog, effect
}

type reconciliationRecordView struct {
	Store
	effects []domain.ToolEffectRecord
}

func (s reconciliationRecordView) ListToolEffects(string) ([]domain.ToolEffectRecord, error) {
	return s.effects, nil
}
func reconciliationCode(err error) string {
	var typed *toolreconciliation.ReconciliationError
	if errors.As(err, &typed) {
		return typed.Code
	}
	return ""
}
