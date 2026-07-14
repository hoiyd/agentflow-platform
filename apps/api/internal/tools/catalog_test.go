package tools

import "testing"

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
