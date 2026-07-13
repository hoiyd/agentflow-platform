package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tools.json")
	if err := os.WriteFile(path, []byte(`{"enabled_tools":["calculator"],"unknown_field":true}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected unknown config field to fail")
	}
}

func TestLoadConfigRejectsTrailingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tools.json")
	if err := os.WriteFile(path, []byte(`{"enabled_tools":["calculator"]} {}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected trailing config data to fail")
	}
}
