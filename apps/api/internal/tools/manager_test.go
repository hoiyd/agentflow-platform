package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestManagerSetEnabledPersistsAndTakesEffectImmediately(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tools.json")
	if err := SaveConfig(path, DefaultConfig()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	manager, err := NewManager(ctx, path, nil)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	items, err := manager.SetEnabled(ctx, "calculator", false)
	if err != nil {
		t.Fatalf("disable calculator: %v", err)
	}
	if enabledFor(items, "calculator") {
		t.Fatal("expected calculator to be disabled in list response")
	}

	registry, err := manager.Registry(ctx)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	if _, ok := registry.Get("calculator"); ok {
		t.Fatal("expected disabled calculator to be unavailable immediately")
	}
	result := registry.Execute(ctx, "calculator", json.RawMessage(`{"expression":"1 + 1"}`))
	if result.Error == "" {
		t.Fatal("expected disabled calculator execution to fail immediately")
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
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tools.json")
	if err := SaveConfig(path, DefaultConfig()); err != nil {
		t.Fatalf("save config: %v", err)
	}

	manager, err := NewManager(ctx, path, nil)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	time.Sleep(10 * time.Millisecond)
	if err := SaveConfig(path, Config{EnabledTools: []string{"get_current_time"}}); err != nil {
		t.Fatalf("save updated config: %v", err)
	}

	registry, err := manager.Registry(ctx)
	if err != nil {
		t.Fatalf("registry: %v", err)
	}
	if _, ok := registry.Get("calculator"); ok {
		t.Fatal("expected externally disabled calculator to be unavailable after reload")
	}
	if _, ok := registry.Get("get_current_time"); !ok {
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
