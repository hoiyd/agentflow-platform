package tool

import (
	"path/filepath"
	"testing"
	"time"
)

func TestManagerOperatorConfigControlsServiceEnablement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tools.json")
	if err := SaveConfig(path, DefaultConfig()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	manager, err := NewManager(path)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	config := DefaultConfig()
	config.EnabledTools = []string{"get_current_time"}
	if err := SaveConfig(path, config); err != nil {
		t.Fatal(err)
	}
	catalog, err := manager.Catalog()
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if _, ok := catalog.Resolve("calculator"); ok {
		t.Fatal("expected disabled calculator to be unavailable immediately")
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	for _, name := range cfg.EnabledTools {
		if name == "calculator" {
			t.Fatal("expected persisted config to omit calculator")
		}
	}
}

func TestManagerReloadsExternalConfigChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tools.json")
	if err := SaveConfig(path, DefaultConfig()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	manager, err := NewManager(path)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	time.Sleep(10 * time.Millisecond)
	if err := SaveConfig(path, Config{EnabledTools: []string{"get_current_time"}}); err != nil {
		t.Fatalf("save updated config: %v", err)
	}

	catalog, err := manager.Catalog()
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if _, ok := catalog.Resolve("calculator"); ok {
		t.Fatal("expected externally disabled calculator to be unavailable after reload")
	}
	if _, ok := catalog.Resolve("get_current_time"); !ok {
		t.Fatal("expected externally enabled get_current_time to remain available")
	}
}

func enabledFor(items []ToolInfo, name string) bool {
	for _, item := range items {
		if item.Name == name {
			return item.Enabled
		}
	}
	return false
}
