package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"agentflow-platform/apps/api/internal/tool/policy"
)

// Failure inventory: explicit rules must not inherit another Tool's targets,
// credentials or resources by array position; omitted defaults remain intact;
// explicit empty/null lists clear grants even after saving; invalid entries/scopes
// still fail. Enabled-Tool lists replace defaults; null/blank entries never
// recover a default name, while omitted/null lists retain existing defaults.
func TestManagerConfigRulesDoNotInheritDefaultCapabilities(t *testing.T) {
	binding := SandboxCommandTool(nil)
	grant := policy.Rule{ID: "operator-sandbox-command", Tool: binding.Descriptor.Name,
		Action: policy.ActionAllowAndLog, Capability: binding.Descriptor.Security}
	defaults := DefaultConfig()
	for position := 0; position <= len(defaults.SecurityPolicy.Rules); position++ {
		t.Run(fmt.Sprintf("sandbox-at-%d", position), func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.EnabledTools = []string{binding.Descriptor.Name}
			rules := append([]policy.Rule(nil), defaults.SecurityPolicy.Rules[:position]...)
			rules = append(rules, grant)
			cfg.SecurityPolicy.Rules = append(rules, defaults.SecurityPolicy.Rules[position:]...)
			path := filepath.Join(t.TempDir(), "tools.json")
			if err := SaveConfig(path, cfg); err != nil {
				t.Fatal(err)
			}
			manager, err := NewManager(path, binding)
			if err != nil {
				t.Fatalf("valid operator policy rejected: %v", err)
			}
			if want := policy.NormalizePolicy(cfg.SecurityPolicy); !reflect.DeepEqual(manager.config.SecurityPolicy, want) {
				t.Fatalf("configuration inherited capabilities: got=%#v want=%#v", manager.config.SecurityPolicy, want)
			}
			decision := policy.Evaluate(manager.config.SecurityPolicy, policy.Request{
				Tool: binding.Descriptor.Name, Declared: binding.Descriptor.Security, RequestedScope: binding.Descriptor.Security.Scope,
			})
			if !decision.Allowed || decision.Action != policy.ActionAllowAndLog || decision.Capability.Scope.Network.Mode != policy.NetworkNone || len(decision.Capability.Scope.Network.Targets) != 0 || len(decision.Capability.Scope.Credentials) != 0 {
				t.Fatalf("sandbox grant changed: %+v", decision)
			}
		})
	}
}

func TestLoadConfigRulePresenceAndRejection(t *testing.T) {
	for _, tc := range []struct {
		name, document        string
		defaultRules, invalid bool
	}{
		{"policy-omitted", `{}`, true, false},
		{"policy-null", `{"security_policy":null}`, true, false},
		{"rules-omitted", `{"security_policy":{}}`, true, false},
		{"scalar-only", `{"security_policy":{"version":"operator-v2"}}`, true, false},
		{"rules-empty", `{"security_policy":{"rules":[]}}`, false, false},
		{"rules-null", `{"security_policy":{"rules":null}}`, false, false},
		{"empty-rule", `{"security_policy":{"rules":[{}]}}`, false, true},
		{"null-rule", `{"security_policy":{"rules":[null]}}`, false, true},
		{"invalid-network", `{"security_policy":{"rules":[{"id":"invalid","tool":"sandbox_command","action":"allow_and_log","capability":{"scope":{"network":{"mode":"none","targets":["example.test"]}}}}]}}`, false, true},
		{"unknown-nested-field", `{"security_policy":{"rules":[{"id":"invalid","tool":"sandbox_command","action":"allow_and_log","capability":{"scope":{"network":{"mode":"none","unknown":true}}}}]}}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tools.json")
			if err := os.WriteFile(path, []byte(tc.document), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(path)
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid configuration retained a default grant")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.defaultRules {
				if !reflect.DeepEqual(cfg.SecurityPolicy.Rules, DefaultConfig().SecurityPolicy.Rules) {
					t.Fatal("omitted default rules changed")
				}
			} else if len(cfg.SecurityPolicy.Rules) != 0 {
				t.Fatal("explicit empty rules restored default grants")
			}
			if cfg.SecurityPolicy.DefaultAction != DefaultConfig().SecurityPolicy.DefaultAction {
				t.Fatal("omitted default action changed")
			}
			if tc.name == "scalar-only" && cfg.SecurityPolicy.Version != "operator-v2" {
				t.Fatal("explicit policy version lost")
			}
		})
	}
}

func TestSaveConfigPreservesExplicitEmptyRules(t *testing.T) {
	for _, rules := range [][]policy.Rule{nil, {}} {
		cfg := DefaultConfig()
		cfg.SecurityPolicy.Rules = rules
		path := filepath.Join(t.TempDir(), "tools.json")
		if err := SaveConfig(path, cfg); err != nil {
			t.Fatal(err)
		}
		loaded, err := LoadConfig(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(loaded.SecurityPolicy.Rules) != 0 {
			t.Fatal("saving an explicit empty policy restored default grants")
		}
	}
}

func TestLoadConfigEnabledToolsReplacement(t *testing.T) {
	for _, tc := range []struct {
		name, document string
		want           []string
		invalid        bool
	}{
		{"omitted", `{}`, DefaultConfig().EnabledTools, false},
		{"null-list", `{"enabled_tools":null}`, DefaultConfig().EnabledTools, false},
		{"empty-list", `{"enabled_tools":[]}`, []string{}, false},
		{"explicit-list", `{"enabled_tools":["get_current_time"]}`, []string{"get_current_time"}, false},
		{"null-entry", `{"enabled_tools":[null]}`, nil, true},
		{"null-after-name", `{"enabled_tools":["calculator",null]}`, nil, true},
		{"empty-name", `{"enabled_tools":[" "]}`, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tools.json")
			if err := os.WriteFile(path, []byte(tc.document), 0600); err != nil {
				t.Fatal(err)
			}
			manager, err := NewManager(path)
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid enabled Tool retained a default name")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(manager.config.EnabledTools, tc.want) {
				t.Fatalf("explicit enabled Tools inherited defaults: got %v want %v", manager.config.EnabledTools, tc.want)
			}
		})
	}
}
