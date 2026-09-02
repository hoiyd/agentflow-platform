package tools

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"agentflow-platform/apps/api/internal/toolpolicy"
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

func TestLoadConfigPreservesDefaultSecurityPolicyWhenOmitted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tools.json")
	if err := os.WriteFile(path, []byte(`{"enabled_tools":["calculator"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(config.SecurityPolicy, toolpolicy.DefaultPolicy()) {
		t.Fatalf("default security policy was lost: %#v", config.SecurityPolicy)
	}
}

func TestLoadConfigRejectsInvalidSecurityPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tools.json")
	data := `{"enabled_tools":["calculator"],"security_policy":{"version":"v1","default_action":"invalid"}}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected invalid security policy to fail")
	}
}

func TestSaveAndLoadSecurityPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tools.json")
	config := DefaultConfig()
	config.SecurityPolicy.Version = "operator-v3"
	config.SecurityPolicy.DefaultAction = toolpolicy.ActionDeny
	if err := SaveConfig(path, config); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, config) {
		t.Fatalf("security policy round trip: got %#v want %#v", loaded, config)
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
