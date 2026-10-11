package tool

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCatalogViewsReuseCompiledContractsAndIsolateState(t *testing.T) {
	catalog := DefaultCatalog()
	compiled := map[*argumentContract]bool{}
	original, _ := catalog.Installed("calculator")
	compiled[original.contract] = true
	for range 8 {
		view, err := catalog.CloneWith()
		if err != nil {
			t.Fatal(err)
		}
		binding, _ := view.Installed("calculator")
		compiled[binding.contract] = true
		if err := view.SetEnabled("calculator", false); err != nil {
			t.Fatal(err)
		}
		if _, ok := catalog.ResolveReady("calculator"); !ok {
			t.Fatal("view disabled the service catalog")
		}
	}
	if len(compiled) != 1 {
		t.Fatalf("read/clone compiled %d contracts, want 1", len(compiled))
	}
	view, err := catalog.CloneWith(original)
	if err == nil || view != nil {
		t.Fatal("duplicate Binding accepted")
	}
	view, err = NewCatalog(original)
	if err != nil {
		t.Fatal(err)
	}
	reused, _ := view.Installed("calculator")
	if reused.contract != original.contract {
		t.Fatal("registered compiled Binding was recompiled")
	}
	for _, parameters := range []map[string]any{
		original.Descriptor.Parameters,
		view.List()[0].Parameters,
		view.Definitions()[0]["function"].(map[string]any)["parameters"].(map[string]any),
	} {
		parameters["type"] = "array"
	}
	installed, _ := catalog.Installed("calculator")
	if installed.Descriptor.Parameters["type"] != "object" {
		t.Fatal("exported schema mutated the catalog")
	}
	installed, _ = view.Installed("calculator")
	if installed.Descriptor.Parameters["type"] != "object" {
		t.Fatal("exported schema mutated the view")
	}
}

func TestCatalogRejectsModifiedCompiledBinding(t *testing.T) {
	catalog, err := NewCatalog(Binding{Descriptor: Descriptor{Name: "lookup", Parameters: ObjectSchema(map[string]any{"id": map[string]any{"type": "string"}}, []string{"id"})}, Handler: func(context.Context, json.RawMessage) (any, error) { return nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	binding, _ := catalog.Installed("lookup")
	binding.Descriptor.Parameters["properties"].(map[string]any)["id"].(map[string]any)["type"] = "integer"
	if _, err := NewCatalog(binding); err == nil {
		t.Fatal("changed schema reused its old validator")
	}
	installed, _ := catalog.Installed("lookup")
	if _, issue := installed.contract.validate(json.RawMessage(`{"id":"valid"}`)); issue != nil {
		t.Fatalf("caller mutated installed validator: %+v", issue)
	}
}

func TestCatalogRecompilesExplicitNewDefinition(t *testing.T) {
	catalog := DefaultCatalog()
	binding, _ := catalog.Installed("calculator")
	oldRevision, oldContract := binding.Descriptor.DefinitionRevision, binding.contract
	binding.Descriptor.DefinitionRevision = ""
	binding.Descriptor.Parameters = ObjectSchema(map[string]any{"value": map[string]any{"type": "integer"}}, []string{"value"})
	updated, err := NewCatalog(binding)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := updated.Installed("calculator")
	if current.contract == oldContract || current.Descriptor.DefinitionRevision == oldRevision {
		t.Fatal("new definition reused stale contract or revision")
	}
	if _, issue := current.contract.validate(json.RawMessage(`{"value":1}`)); issue != nil {
		t.Fatalf("new schema not applied: %+v", issue)
	}
	if _, issue := current.contract.validate(json.RawMessage(`{"value":"old"}`)); issue == nil {
		t.Fatal("new schema accepted invalid arguments")
	}
}
