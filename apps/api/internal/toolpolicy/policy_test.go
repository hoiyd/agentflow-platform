package toolpolicy

import (
	"reflect"
	"strings"
	"testing"
)

func TestEvaluateDefaultPolicy(t *testing.T) {
	tests := []struct {
		name       string
		request    Request
		wantAction Action
		wantReason string
		allowed    bool
	}{
		{name: "local computation", request: Request{Tool: "calculator", Declared: Capability{}, RequestedScope: Scope{}}, wantAction: ActionAllow, wantReason: "policy_allowed", allowed: true},
		{name: "remote requires rule", request: Request{Tool: "remote_reader", Declared: Capability{Source: SourceRemote}, RequestedScope: Scope{}}, wantAction: ActionDeny, wantReason: "remote_tool_requires_rule"},
		{name: "write requires rule", request: Request{Tool: "writer", Declared: Capability{Scope: Scope{Resources: []ResourceScope{{Kind: ResourceWorkspace, Name: "records", Access: AccessWrite}}}, SideEffect: SideEffectInternalWrite, Reversibility: Compensatable}, RequestedScope: Scope{Resources: []ResourceScope{{Kind: ResourceWorkspace, Name: "records", Access: AccessWrite}}}}, wantAction: ActionDeny, wantReason: "sensitive_scope_requires_rule"},
		{name: "task state explicit rule", request: Request{Tool: "update_task_state", Declared: DefaultPolicy().Rules[0].Capability, RequestedScope: DefaultPolicy().Rules[0].Capability.Scope}, wantAction: ActionAllowAndLog, wantReason: "policy_allowed", allowed: true},
		{name: "missing credential", request: Request{Tool: "secret_reader", Declared: Capability{Scope: Scope{Credentials: []string{"search_api"}}}, RequestedScope: Scope{Credentials: []string{"search_api"}}}, wantAction: ActionDeny, wantReason: "credential_scope_unavailable"},
		{name: "scope expansion", request: Request{Tool: "reader", Declared: Capability{}, RequestedScope: Scope{Network: NetworkScope{Mode: NetworkExternal, Targets: []string{"example.com"}}}}, wantAction: ActionDeny, wantReason: "binding_scope_expansion"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := Evaluate(DefaultPolicy(), test.request)
			if decision.Action != test.wantAction || decision.Reason != test.wantReason || decision.Allowed != test.allowed {
				t.Fatalf("decision = %#v", decision)
			}
		})
	}
}

func TestEvaluateExplicitCredentialAndIrreversibleRules(t *testing.T) {
	credentialCapability := NormalizeCapability(Capability{
		Scope:      Scope{Credentials: []string{"payments"}, Network: NetworkScope{Mode: NetworkExternal, Targets: []string{"api.example.com"}}},
		SideEffect: SideEffectExternalWrite, Reversibility: Irreversible, Visibility: VisibilityOperator, Audit: AuditFull,
	})
	policy := Policy{Version: "operator-v7", DefaultAction: ActionDeny, Rules: []Rule{{
		ID: "charge", Tool: "charge_card", Action: ActionAllowAndLog, Capability: credentialCapability,
	}}}
	request := Request{Tool: "charge_card", Declared: credentialCapability, RequestedScope: credentialCapability.Scope, AvailableCredentialScopes: []string{"payments"}}
	decision := Evaluate(policy, request)
	if !decision.Allowed || !decision.AuditRequired() || decision.RuleID != "charge" || decision.PolicyVersion != "operator-v7" {
		t.Fatalf("explicit irreversible decision = %#v", decision)
	}

	policy.Rules[0].Action = ActionAllow
	decision = Evaluate(policy, request)
	if decision.Allowed || decision.Reason != "irreversible_action_requires_logged_authorization" {
		t.Fatalf("plain allow authorized irreversible action: %#v", decision)
	}
}

func TestEvaluateApprovalActionsRemainBlocked(t *testing.T) {
	capability := NormalizeCapability(Capability{Approval: ApprovalAsk})
	for _, action := range []Action{ActionAsk, ActionHumanOnly} {
		policy := Policy{Version: "v1", DefaultAction: ActionDeny, Rules: []Rule{{ID: string(action), Tool: "approval_tool", Action: action, Capability: capability}}}
		decision := Evaluate(policy, Request{Tool: "approval_tool", Declared: capability, RequestedScope: capability.Scope})
		if decision.Allowed || decision.Action != action || decision.Reason != "human_approval_required" {
			t.Fatalf("%s decision = %#v", action, decision)
		}
	}
}

func TestNormalizeAndValidatePolicy(t *testing.T) {
	capability := NormalizeCapability(Capability{Scope: Scope{
		Resources: []ResourceScope{
			{Kind: ResourceWorkspace, Name: " beta ", Access: AccessRead},
			{Kind: ResourceRun, Name: "artifact", Access: AccessWrite},
			{Kind: ResourceRun, Name: "artifact", Access: AccessRead},
			{Kind: ResourceRun, Name: "artifact", Access: AccessRead},
		},
		Credentials: []string{" beta ", "alpha", "alpha"},
	}})
	if got, want := capability.Scope.Credentials, []string{"alpha", "beta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("credentials = %#v want %#v", got, want)
	}
	if len(capability.Scope.Resources) != 3 || capability.Scope.Resources[0].Kind != ResourceRun ||
		capability.Scope.Resources[0].Access != AccessRead || capability.Scope.Resources[2].Name != "beta" {
		t.Fatalf("resources were not deduplicated: %#v", capability.Scope.Resources)
	}

	tests := []Policy{
		{},
		{Version: "v1", DefaultAction: "invalid"},
		{Version: "v1", DefaultAction: ActionDeny, Rules: []Rule{{ID: "", Tool: "missing-id", Action: ActionAllow}}},
		{Version: "v1", DefaultAction: ActionDeny, Rules: []Rule{{ID: "missing-tool", Tool: "", Action: ActionAllow}}},
		{Version: "v1", DefaultAction: ActionDeny, Rules: []Rule{{ID: "bad-action", Tool: "one", Action: "invalid"}}},
		{Version: "v1", DefaultAction: ActionDeny, Rules: []Rule{{ID: "same", Tool: "one", Action: ActionAllow}, {ID: "same", Tool: "two", Action: ActionAllow}}},
		{Version: "v1", DefaultAction: ActionDeny, Rules: []Rule{{ID: "one", Tool: "same", Action: ActionAllow}, {ID: "two", Tool: "same", Action: ActionAllow}}},
		{Version: "v1", DefaultAction: ActionDeny, Rules: []Rule{{ID: "bad", Tool: "bad", Action: ActionAllow, Capability: Capability{Scope: Scope{Network: NetworkScope{Mode: NetworkExternal}}}}}},
	}
	for index, policy := range tests {
		if err := ValidatePolicy(policy); err == nil {
			t.Fatalf("policy %d unexpectedly passed", index)
		}
	}
}

func TestValidateCapabilityRejectsInvalidDimensions(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Capability)
		want   string
	}{
		{name: "source", mutate: func(value *Capability) { value.Source = "invalid" }, want: "source"},
		{name: "side effect", mutate: func(value *Capability) { value.SideEffect = "invalid" }, want: "side-effect"},
		{name: "rate", mutate: func(value *Capability) { value.Rate = "invalid" }, want: "rate"},
		{name: "reversibility", mutate: func(value *Capability) { value.Reversibility = "invalid" }, want: "reversibility"},
		{name: "visibility", mutate: func(value *Capability) { value.Visibility = "invalid" }, want: "visibility"},
		{name: "approval", mutate: func(value *Capability) { value.Approval = "invalid" }, want: "approval"},
		{name: "audit", mutate: func(value *Capability) { value.Audit = "invalid" }, want: "audit"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			capability := NormalizeCapability(Capability{})
			test.mutate(&capability)
			if err := ValidateCapability(capability); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q validation error, got %v", test.want, err)
			}
		})
	}
}

func TestValidateScopeRejectsInvalidAuthority(t *testing.T) {
	tests := []struct {
		name  string
		scope Scope
	}{
		{name: "network mode", scope: Scope{Network: NetworkScope{Mode: "invalid"}}},
		{name: "targets without network", scope: Scope{Network: NetworkScope{Mode: NetworkNone, Targets: []string{"example.com"}}}},
		{name: "network without targets", scope: Scope{Network: NetworkScope{Mode: NetworkExternal}}},
		{name: "resource kind", scope: Scope{Resources: []ResourceScope{{Kind: "invalid", Name: "record", Access: AccessRead}}}},
		{name: "resource name", scope: Scope{Resources: []ResourceScope{{Kind: ResourceRun, Access: AccessRead}}}},
		{name: "resource access", scope: Scope{Resources: []ResourceScope{{Kind: ResourceRun, Name: "record", Access: "invalid"}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateScope(test.scope); err == nil {
				t.Fatal("invalid scope unexpectedly passed")
			}
		})
	}
}

func TestEvaluateFailClosedBranches(t *testing.T) {
	decision := Evaluate(DefaultPolicy(), Request{})
	if decision.Reason != "capability_invalid" {
		t.Fatalf("empty Tool decision = %#v", decision)
	}

	approval := NormalizeCapability(Capability{Approval: ApprovalAsk})
	policy := Policy{Version: "v1", DefaultAction: ActionDeny, Rules: []Rule{{
		ID: "approval", Tool: "approval_tool", Action: ActionAllow, Capability: approval,
	}}}
	decision = Evaluate(policy, Request{Tool: "approval_tool", Declared: approval, RequestedScope: approval.Scope})
	if decision.Reason != "approval_policy_not_satisfied" || decision.Allowed {
		t.Fatalf("approval decision = %#v", decision)
	}

	decision = Evaluate(Policy{Version: "v1", DefaultAction: ActionDeny}, Request{Tool: "calculator"})
	if decision.Reason != "policy_denied" || decision.Action != ActionDeny {
		t.Fatalf("default deny decision = %#v", decision)
	}

	for _, resource := range []ResourceScope{
		{Kind: ResourceFilesystem, Name: "workspace", Access: AccessRead},
		{Kind: ResourceExternal, Name: "service", Access: AccessRead},
	} {
		capability := NormalizeCapability(Capability{Scope: Scope{Resources: []ResourceScope{resource}}})
		decision = Evaluate(DefaultPolicy(), Request{Tool: "reader", Declared: capability, RequestedScope: capability.Scope})
		if decision.Reason != "sensitive_scope_requires_rule" {
			t.Fatalf("sensitive resource decision = %#v", decision)
		}
	}
}

func TestEvaluateRejectsRuleScopeMismatchAndInvalidPolicy(t *testing.T) {
	declared := NormalizeCapability(Capability{Scope: Scope{Resources: []ResourceScope{{Kind: ResourceWorkspace, Name: "documents", Access: AccessRead}}}})
	policy := Policy{Version: "v1", DefaultAction: ActionDeny, Rules: []Rule{{ID: "narrow", Tool: "reader", Action: ActionAllow, Capability: NormalizeCapability(Capability{})}}}
	decision := Evaluate(policy, Request{Tool: "reader", Declared: declared, RequestedScope: declared.Scope})
	if decision.Reason != "rule_scope_mismatch" {
		t.Fatalf("scope mismatch decision = %#v", decision)
	}
	decision = Evaluate(Policy{Version: "v1", DefaultAction: "broken"}, Request{Tool: "reader", Declared: Capability{}, RequestedScope: Scope{}})
	if decision.Reason != "policy_invalid" {
		t.Fatalf("invalid policy decision = %#v", decision)
	}
}
