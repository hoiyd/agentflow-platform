package store

import (
	"context"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
	"agentflow-platform/apps/api/internal/tool/reconciliation"
)

// An explicit authenticated recovery command is its own confirmation, not a
// model Tool grant. It still needs the exact receipt/version and command dedupe.
func TestReconciliationApprovalRequiredUsesExplicitCommand(t *testing.T) {
	calls := 0
	var expectedKey string
	target, run, catalog, effect := postgresReconciliationFixture(t, tool.SideEffectReconciliation{
		RetryWithSameKey: func(_ context.Context, recovery tool.EffectReconciliationContext) (any, error) {
			calls++
			if recovery.IdempotencyKey != expectedKey {
				t.Errorf("changed idempotency key: %s", recovery.IdempotencyKey)
			}
			return map[string]any{"confirmed": true}, nil
		},
	})
	expectedKey = effect.IdempotencyKey
	binding, _ := catalog.Resolve(effect.ToolName)
	config := catalog.SecurityPolicy()
	config.Rules[0].Action = policy.ActionApprovalRequired
	var err error
	catalog, err = tool.NewCatalogWithPolicy(config, binding)
	if err != nil {
		t.Fatal(err)
	}
	command := retryCommand(effect)
	for i := 0; i < 2; i++ {
		outcome, err := reconciliation.ReconcileToolEffect(t.Context(), catalog, target, run, effect.IdempotencyKey, command)
		if err != nil || outcome.Effect.Status != domain.ToolEffectCommitted {
			t.Fatalf("explicit recovery: %#v %v", outcome, err)
		}
	}
	if calls != 1 {
		t.Fatalf("duplicate explicit command executed %d callbacks", calls)
	}
}
