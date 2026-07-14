package tools

import "testing"

func TestBuildCatalogEnablesOnlyConfiguredTools(t *testing.T) {
	catalog, err := BuildCatalog(Config{EnabledTools: []string{"calculator"}})
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if names := catalog.EnabledNames(); len(names) != 1 || names[0] != "calculator" {
		t.Fatalf("expected only calculator, got %#v", names)
	}
}

func TestBuildCatalogRejectsUnknownConfiguredTool(t *testing.T) {
	if _, err := BuildCatalog(Config{EnabledTools: []string{"calculator", "removed_tool"}}); err == nil {
		t.Fatal("expected unknown configured tool to fail")
	}
}
