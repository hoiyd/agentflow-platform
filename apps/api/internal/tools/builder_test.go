package tools

import (
	"testing"

	"agentflow-platform/apps/api/internal/toolpolicy"
)

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

func TestBuildCatalogRejectsInvalidSecurityPolicy(t *testing.T) {
	_, err := BuildCatalog(Config{
		EnabledTools:   []string{"calculator"},
		SecurityPolicy: toolpolicy.Policy{Version: "v1", DefaultAction: "invalid"},
	})
	if err == nil {
		t.Fatal("expected invalid Tool security policy to fail")
	}
}
