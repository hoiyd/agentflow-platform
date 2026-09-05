package tools

import (
	"context"
	"encoding/json"
	"testing"

	"agentflow-platform/apps/api/internal/toolpolicy"
)

func TestDefaultCatalogDefinitions(t *testing.T) {
	catalog := DefaultCatalog()
	definitions := catalog.Definitions()
	if len(definitions) != 2 {
		t.Fatalf("expected 2 tool definitions, got %d", len(definitions))
	}

	names := map[string]bool{}
	for _, definition := range definitions {
		function := definition["function"].(map[string]any)
		names[function["name"].(string)] = true
	}
	for _, name := range []string{"calculator", "get_current_time"} {
		if !names[name] {
			t.Fatalf("expected definition for %s", name)
		}
	}
}

func TestCurrentTimeIsEnabledByDefault(t *testing.T) {
	if _, ok := DefaultCatalog().Resolve("get_current_time"); !ok {
		t.Fatal("expected get_current_time to be enabled by default")
	}
}

func TestCatalogKeepsDescriptorSeparateFromBinding(t *testing.T) {
	binding, ok := DefaultCatalog().Installed("calculator")
	if !ok {
		t.Fatal("expected calculator binding")
	}
	if binding.Descriptor.Name != "calculator" || binding.Handler == nil {
		t.Fatalf("unexpected binding: %#v", binding)
	}
}

func TestCatalogRequiresFrozenReconciliationCapabilityToMatchCallbacks(t *testing.T) {
	handler := func(context.Context, json.RawMessage) (any, error) { return nil, nil }
	retry := func(context.Context, EffectReconciliationContext) (any, error) { return nil, nil }
	compensate := func(context.Context, EffectReconciliationContext) error { return nil }
	tests := []Binding{
		{Descriptor: Descriptor{Name: "non_external", Parameters: ObjectSchema(nil, nil), SideEffect: SideEffectPolicy{RetryWithSameKey: true}}, Handler: handler, Reconciliation: SideEffectReconciliation{RetryWithSameKey: retry}},
		{Descriptor: Descriptor{Name: "callback_without_capability", Parameters: ObjectSchema(nil, nil), SideEffect: SideEffectPolicy{Mode: SideEffectExternal}, Security: externalCapability(toolpolicy.Compensatable)}, Handler: handler, Reconciliation: SideEffectReconciliation{RetryWithSameKey: retry}},
		{Descriptor: Descriptor{Name: "capability_without_callback", Parameters: ObjectSchema(nil, nil), SideEffect: SideEffectPolicy{Mode: SideEffectExternal, RetryWithSameKey: true}, Security: externalCapability(toolpolicy.Compensatable)}, Handler: handler},
		{Descriptor: Descriptor{Name: "compensation_without_callback", Parameters: ObjectSchema(nil, nil), SideEffect: SideEffectPolicy{Mode: SideEffectExternal, Compensate: true}, Security: externalCapability(toolpolicy.Compensatable)}, Handler: handler},
		{Descriptor: Descriptor{Name: "irreversible_compensation", Parameters: ObjectSchema(nil, nil), SideEffect: SideEffectPolicy{Mode: SideEffectExternal, Compensate: true}, Security: externalCapability(toolpolicy.Irreversible)}, Handler: handler, Reconciliation: SideEffectReconciliation{Compensate: compensate}},
	}
	for _, binding := range tests {
		if _, err := NewCatalog(binding); err == nil {
			t.Fatalf("expected reconciliation contract failure for %s", binding.Descriptor.Name)
		}
	}
}

func TestReconciliationCapabilityChangesDefinitionRevision(t *testing.T) {
	handler := func(context.Context, json.RawMessage) (any, error) { return nil, nil }
	descriptor := Descriptor{
		Name: "writer", Parameters: ObjectSchema(nil, nil),
		SideEffect: SideEffectPolicy{Mode: SideEffectExternal}, Security: externalCapability(toolpolicy.Compensatable),
	}
	without, err := NewCatalog(Binding{Descriptor: descriptor, Handler: handler})
	if err != nil {
		t.Fatal(err)
	}
	descriptor.SideEffect.RetryWithSameKey = true
	with, err := NewCatalog(Binding{
		Descriptor: descriptor, Handler: handler,
		Reconciliation: SideEffectReconciliation{RetryWithSameKey: func(context.Context, EffectReconciliationContext) (any, error) { return nil, nil }},
	})
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := without.Installed("writer")
	retryable, _ := with.Installed("writer")
	if plain.Descriptor.DefinitionRevision == retryable.Descriptor.DefinitionRevision {
		t.Fatal("reconciliation capability did not change frozen Tool definition revision")
	}
}
