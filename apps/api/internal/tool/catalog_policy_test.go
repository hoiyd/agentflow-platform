package tool

import (
	"testing"

	"agentflow-platform/apps/api/internal/tool/policy"
)

func TestWithRulesAddsBindingsWithoutOverridingOperatorPolicy(t *testing.T) {
	binding, _ := DefaultCatalog().Installed("calculator")
	base, err := NewCatalogWithPolicy(policy.Policy{Rules: []policy.Rule{{
		ID: "operator-deny", Tool: "calculator", Action: policy.ActionDeny,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	rules := []policy.Rule{{ID: "workspace-allow", Tool: "calculator", Action: policy.ActionAllowAndLog}}
	view, err := base.WithRules(rules, binding)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := view.ResolveReady("calculator"); !ok {
		t.Fatal("added binding is missing")
	}
	if got := view.SecurityPolicy().Rules; len(got) != 1 || got[0].Action != policy.ActionDeny {
		t.Fatalf("workspace rule replaced operator deny: %#v", got)
	}
	if _, ok := base.Installed("calculator"); ok || len(base.SecurityPolicy().Rules) != 1 {
		t.Fatal("base catalog was modified")
	}
	binding.Handler = nil
	if _, err := base.WithRules(rules, binding); err == nil {
		t.Fatal("invalid binding bypassed registration")
	}
}
