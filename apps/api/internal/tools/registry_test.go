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
