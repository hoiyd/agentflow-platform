package store

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/toolreconciliation"
	"agentflow-platform/apps/api/internal/tools"
)

// Both callers must pass the read checks before either can claim/settle.
type commitBarrier struct {
	Store
	arrived chan struct{}
	release chan struct{}
	phase   domain.RunEventType
}

func (s commitBarrier) CommitToolEffectReconciliation(m domain.ToolEffectReconciliation) (domain.ToolEffectRecord, domain.RunEvent, bool, error) {
	if m.Event.Type == s.phase {
		s.arrived <- struct{}{}
		<-s.release
	}
	return s.Store.CommitToolEffectReconciliation(m)
}

func TestReconciliationCASAfterCompetingPreflightReads(t *testing.T) {
	for _, backend := range []string{"file", "postgres"} {
		for _, variant := range []string{"same-command", "different-command", "changed-payload", "manual-changed-payload"} {
			t.Run(backend+"/"+variant, func(t *testing.T) {
				var calls atomic.Int32
				target, _, run, catalog, effect := safetyFixture(t, backend, tools.SideEffectReconciliation{RetryWithSameKey: func(context.Context, tools.EffectReconciliationContext) (any, error) { calls.Add(1); return true, nil }})
				first := retryCommand(effect)
				phase := domain.EventToolEffectReconciliationStarted
				if variant == "manual-changed-payload" {
					first.Action = domain.ToolEffectConfirmFailed
					phase = domain.EventToolEffectReconciled
				}
				second := first
				if variant == "different-command" {
					second.CommandID = "other"
				}
				if strings.Contains(variant, "changed-payload") {
					second.Reason = "different intent"
				}
				barrier := commitBarrier{target, make(chan struct{}, 2), make(chan struct{}), phase}
				done := make(chan error, 2)
				for _, cmd := range []toolreconciliation.ToolEffectReconciliationCommand{first, second} {
					go func(command toolreconciliation.ToolEffectReconciliationCommand) {
						_, err := toolreconciliation.ReconcileToolEffect(context.Background(), catalog, barrier, run, effect.IdempotencyKey, command)
						done <- err
					}(cmd)
				}
				for range 2 {
					select {
					case <-barrier.arrived:
					case <-time.After(5 * time.Second):
						close(barrier.release)
						t.Fatal("preflight barrier not reached")
					}
				}
				close(barrier.release)
				failures := 0
				for range 2 {
					if err := <-done; err != nil {
						failures++
						if !IsToolEffectConflict(err) && reconciliationCode(err) != toolreconciliation.ReconciliationConflict {
							t.Fatal(err)
						}
					}
				}
				wantFailures := 1
				if variant == "same-command" {
					wantFailures = 0
				}
				if failures != wantFailures {
					t.Fatalf("conflicts=%d want=%d", failures, wantFailures)
				}
				wantCalls := int32(1)
				if variant == "manual-changed-payload" {
					wantCalls = 0
				}
				if calls.Load() != wantCalls {
					t.Fatalf("calls=%d want=%d", calls.Load(), wantCalls)
				}
			})
		}
	}
}

func TestCallbackDeadlineAndCompensationIdentity(t *testing.T) {
	t.Run("deadline", func(t *testing.T) {
		target, run, catalog, effect := fileReconciliationFixture(t, tools.SideEffectReconciliation{RetryWithSameKey: func(ctx context.Context, _ tools.EffectReconciliationContext) (any, error) {
			<-ctx.Done()
			return true, nil
		}})
		binding, _ := catalog.Resolve(effect.ToolName)
		binding.Policy.Timeout = 10 * time.Millisecond
		catalog, err := tools.NewCatalogWithPolicy(catalog.SecurityPolicy(), binding)
		if err != nil {
			t.Fatal(err)
		}
		result, err := toolreconciliation.ReconcileToolEffect(context.Background(), catalog, target, run, effect.IdempotencyKey, retryCommand(effect))
		if err != nil || result.Outcome != "failed" || result.Effect.Status != domain.ToolEffectReconciling || !strings.Contains(result.Effect.Error, "deadline") {
			t.Fatalf("deadline: %#v %v", result, err)
		}
	})
	t.Run("stable compensation key", func(t *testing.T) {
		keys := make(chan string, 2)
		target, run, catalog, effect := fileReconciliationFixture(t, tools.SideEffectReconciliation{Compensate: func(_ context.Context, recovery tools.EffectReconciliationContext) error {
			keys <- recovery.CompensationKey
			return errors.New("temporary failure")
		}})
		command := retryCommand(effect)
		command.Action = domain.ToolEffectCompensate
		first, err := toolreconciliation.ReconcileToolEffect(context.Background(), catalog, target, run, effect.IdempotencyKey, command)
		if err != nil {
			t.Fatal(err)
		}
		command.CommandID, command.ExpectedVersion = "another-command", first.Effect.Version
		if _, err := toolreconciliation.ReconcileToolEffect(context.Background(), catalog, target, run, effect.IdempotencyKey, command); err != nil {
			t.Fatal(err)
		}
		if firstKey, secondKey := <-keys, <-keys; firstKey == "" || firstKey != secondKey {
			t.Fatalf("compensation identity changed: %s %s", firstKey, secondKey)
		}
	})
}

func TestCanceledRequestDoesNotClaimAndCallbackResultIsGoverned(t *testing.T) {
	for _, variant := range []string{"canceled", "panic", "large", "redacted"} {
		t.Run(variant, func(t *testing.T) {
			target, run, catalog, effect := fileReconciliationFixture(t, tools.SideEffectReconciliation{RetryWithSameKey: func(context.Context, tools.EffectReconciliationContext) (any, error) {
				switch variant {
				case "canceled":
					t.Error("canceled request invoked callback")
				case "panic":
					panic("Bearer panic-secret")
				case "large":
					return strings.Repeat("x", tools.DefaultMaxResultBytes), nil
				}
				return map[string]any{"api_key": "callback-secret", "value": "Bearer result-secret"}, nil
			}})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if variant == "canceled" {
				cancel()
			}
			result, err := toolreconciliation.ReconcileToolEffect(ctx, catalog, target, run, effect.IdempotencyKey, retryCommand(effect))
			if variant == "canceled" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				events, _ := target.ListRunEvents(run.ID)
				if len(events) != 0 {
					t.Fatal("canceled request persisted claim")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if variant != "redacted" && result.Outcome != "failed" {
				t.Fatalf("unexpected outcome %#v", result)
			}
			records, _ := target.ListToolEffects(run.ID)
			if strings.Contains(string(records[0].Result)+records[0].Error, "-secret") {
				t.Fatal("secret persisted")
			}
		})
	}
}
