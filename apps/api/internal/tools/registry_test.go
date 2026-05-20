package tools

import "testing"

func TestDefaultRegistryDefinitions(t *testing.T) {
	registry := DefaultRegistry()
	definitions := registry.Definitions()
	if len(definitions) != 3 {
		t.Fatalf("expected 3 tool definitions, got %d", len(definitions))
	}

	names := map[string]bool{}
	for _, definition := range definitions {
		fn := definition["function"].(map[string]any)
		names[fn["name"].(string)] = true
	}
	for _, name := range []string{"calculator", "get_current_time", "mock_web_search"} {
		if !names[name] {
			t.Fatalf("expected definition for %s", name)
		}
	}
}

func TestEnabledSubsetUsesOnlyRequestedEnabledTools(t *testing.T) {
	registry := DefaultRegistry()
	if err := registry.SetEnabled("mock_web_search", false); err != nil {
		t.Fatalf("disable mock search: %v", err)
	}

	subset, err := registry.EnabledSubset([]string{"calculator", "mock_web_search", "missing"})
	if err != nil {
		t.Fatalf("enabled subset: %v", err)
	}

	names := subset.EnabledNames()
	if len(names) != 1 || names[0] != "calculator" {
		t.Fatalf("expected only calculator, got %#v", names)
	}
}
