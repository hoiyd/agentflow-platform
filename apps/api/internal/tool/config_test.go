package tool

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/tool/policy"
)

func TestApprovalRequiredConfigRoundTrip(t *testing.T) {
	// Literal prior config, not current defaults: old spellings remain readable
	// and stable across save/reload; new configs use the explicit action name.
	const prior = `{"enabled_tools":["calculator"],"security_policy":{"version":"operator-v1","default_action":"ask","rules":[{"id":"review","tool":"calculator","action":"ask","capability":{"approval_mode":"ask"}}]}}`
	for _, spelling := range []string{"ask", "approval_required"} {
		t.Run(spelling, func(t *testing.T) {
			fixture := strings.ReplaceAll(prior, `"ask"`, `"`+spelling+`"`)
			path := filepath.Join(t.TempDir(), "tools.json")
			if err := os.WriteFile(path, []byte(fixture), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(cfg.SecurityPolicy)
			if err = SaveConfig(path, cfg); err != nil {
				t.Fatal(err)
			}
			loaded, err := LoadConfig(path)
			if err != nil {
				t.Fatal(err)
			}
			after, _ := json.Marshal(loaded.SecurityPolicy)
			if !bytes.Equal(before, after) || loaded.SecurityPolicy.DefaultAction != policy.Action(spelling) || loaded.SecurityPolicy.Rules[0].Capability.Approval != policy.ApprovalMode(spelling) {
				t.Fatalf("policy changed on restart: %s -> %s", before, after)
			}
			decision := policy.Evaluate(loaded.SecurityPolicy, policy.Request{Tool: "calculator", Declared: loaded.SecurityPolicy.Rules[0].Capability})
			if decision.Allowed || decision.Action != "approval_required" || !policy.CanRequestApproval(decision) {
				t.Fatalf("unexpected decision: %#v", decision)
			}
		})
	}
}

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
	if !reflect.DeepEqual(config.SecurityPolicy, DefaultConfig().SecurityPolicy) {
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
	config.SecurityPolicy.DefaultAction = policy.ActionDeny
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
