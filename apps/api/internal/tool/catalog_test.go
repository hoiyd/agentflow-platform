package tool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"agentflow-platform/apps/api/internal/tool/policy"
)

func TestDefaultCatalogDefinitions(t *testing.T) {
	catalog := DefaultCatalog()
	definitions := catalog.Definitions()
	if len(definitions) != 2 {
		t.Fatalf("expected 2 ready tool definitions, got %d", len(definitions))
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

func TestCatalogDerivesJournalFromSecurity(t *testing.T) {
	for _, class := range []policy.SideEffectClass{policy.SideEffectNone, policy.SideEffectInternalWrite, policy.SideEffectExternalWrite, policy.SideEffectDestructive} {
		t.Run(string(class), func(t *testing.T) {
			binding := Binding{Descriptor: Descriptor{Name: "classified", Parameters: ObjectSchema(nil, nil), Security: policy.Capability{SideEffect: class}},
				Handler: func(context.Context, json.RawMessage) (any, error) { return nil, nil }}
			if _, err := NewCatalog(binding); err != nil {
				t.Fatalf("one authoritative security classification must suffice: %v", err)
			}
		})
	}
}

func TestDerivedJournalPreservesFrozenDefinition(t *testing.T) {
	for _, test := range []struct {
		class policy.SideEffectClass
		mode  string
	}{{policy.SideEffectNone, ""}, {policy.SideEffectInternalWrite, "internal"}, {policy.SideEffectExternalWrite, "external"}, {policy.SideEffectDestructive, "external"}} {
		t.Run(string(test.class), func(t *testing.T) {
			catalog, err := NewCatalog(Binding{Descriptor: Descriptor{Name: "frozen", Parameters: ObjectSchema(nil, nil), Security: policy.Capability{SideEffect: test.class}},
				Handler: func(context.Context, json.RawMessage) (any, error) { return nil, nil }})
			if err != nil {
				t.Fatal(err)
			}
			binding, _ := catalog.Installed("frozen")
			d := binding.Descriptor
			// This is the pre-refactor digest's wire shape and field order, not
			// the new implementation's serializer. Existing frozen Runs must match.
			legacy := struct {
				SchemaVersion string            `json:"schema_version"`
				Name          string            `json:"name"`
				Description   string            `json:"description"`
				Parameters    map[string]any    `json:"parameters"`
				Concurrency   ConcurrencyPolicy `json:"concurrency"`
				SideEffect    struct {
					Mode string `json:"mode,omitempty"`
				} `json:"side_effect"`
				Security policy.Capability `json:"security"`
			}{SchemaVersion: d.SchemaVersion, Name: d.Name, Description: d.Description, Parameters: d.Parameters, Concurrency: d.Concurrency, Security: d.Security}
			legacy.SideEffect.Mode = test.mode
			encoded, err := json.Marshal(legacy)
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(encoded)
			if d.DefinitionRevision != "sha256:"+hex.EncodeToString(digest[:]) {
				t.Fatalf("frozen definition drifted for %s: %s", test.class, d.DefinitionRevision)
			}
		})
	}
}

func TestCatalogRequiresFrozenReconciliationCapabilityToMatchCallbacks(t *testing.T) {
	handler := func(context.Context, json.RawMessage) (any, error) { return nil, nil }
	retry := func(context.Context, EffectReconciliationContext) (any, error) { return nil, nil }
	compensate := func(context.Context, EffectReconciliationContext) error { return nil }
	tests := []Binding{
		{Descriptor: Descriptor{Name: "non_external", Parameters: ObjectSchema(nil, nil), SideEffect: SideEffectPolicy{RetryWithSameKey: true}}, Handler: handler, Reconciliation: SideEffectReconciliation{RetryWithSameKey: retry}},
		{Descriptor: Descriptor{Name: "callback_without_capability", Parameters: ObjectSchema(nil, nil), Security: externalCapability(policy.Compensatable)}, Handler: handler, Reconciliation: SideEffectReconciliation{RetryWithSameKey: retry}},
		{Descriptor: Descriptor{Name: "capability_without_callback", Parameters: ObjectSchema(nil, nil), SideEffect: SideEffectPolicy{RetryWithSameKey: true}, Security: externalCapability(policy.Compensatable)}, Handler: handler},
		{Descriptor: Descriptor{Name: "compensation_without_callback", Parameters: ObjectSchema(nil, nil), SideEffect: SideEffectPolicy{Compensate: true}, Security: externalCapability(policy.Compensatable)}, Handler: handler},
		{Descriptor: Descriptor{Name: "irreversible_compensation", Parameters: ObjectSchema(nil, nil), SideEffect: SideEffectPolicy{Compensate: true}, Security: externalCapability(policy.Irreversible)}, Handler: handler, Reconciliation: SideEffectReconciliation{Compensate: compensate}},
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
		Security: externalCapability(policy.Compensatable),
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

func TestCatalogRejectsInvalidSideEffectClasses(t *testing.T) {
	for _, class := range []policy.SideEffectClass{"", "internal", "external", "unsupported"} {
		_, err := NewCatalog(Binding{Descriptor: Descriptor{Name: "writer", Parameters: ObjectSchema(nil, nil), Security: policy.Capability{SideEffect: class}},
			Handler: func(context.Context, json.RawMessage) (any, error) { return nil, nil }})
		if (err == nil) != (class == "") {
			t.Fatalf("class=%q: only omitted read-only defaults are valid: %v", class, err)
		}
	}
}
