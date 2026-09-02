package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/toolpolicy"
)

func TestExecutorSecurityPolicyDeniesBeforeBudgetAndHandler(t *testing.T) {
	var calls atomic.Int32
	capability := externalCapability(toolpolicy.Compensatable)
	binding := Binding{
		Descriptor: Descriptor{Name: "unapproved_writer", Parameters: ObjectSchema(nil, nil), Security: capability},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			calls.Add(1)
			return map[string]any{"ok": true}, nil
		},
	}
	catalog, err := NewCatalogWithPolicy(toolpolicy.DefaultPolicy(), binding)
	if err != nil {
		t.Fatal(err)
	}
	controller := &toolBudgetController{}
	result := NewExecutor(catalog, ExecutorOptions{}).Execute(
		budget.WithController(context.Background(), controller),
		ExecutionRequest{CallID: "call-1", Tool: "unapproved_writer"},
	)
	if result.Error == nil || result.Error.Code != ErrorSecurityPolicyDenied || calls.Load() != 0 || controller.calls != 0 {
		t.Fatalf("denial crossed enforcement boundary: result=%#v handler=%d budget=%d", result, calls.Load(), controller.calls)
	}
	if result.PolicyDecision == nil || result.PolicyDecision.Reason != "sensitive_scope_requires_rule" {
		t.Fatalf("denial is not explainable: %#v", result.PolicyDecision)
	}
	if result.Error.FailureInfo().Category != "validation" {
		t.Fatalf("security denial failure category = %#v", result.Error.FailureInfo())
	}
}

func TestExecutorSecurityPolicyRequiresCredentialGrant(t *testing.T) {
	capability := toolpolicy.NormalizeCapability(toolpolicy.Capability{
		Scope: toolpolicy.Scope{
			Network:     toolpolicy.NetworkScope{Mode: toolpolicy.NetworkExternal, Targets: []string{"api.example.com"}},
			Credentials: []string{"search_api"},
		},
	})
	policy := policyFor("search", toolpolicy.ActionAllow, capability)
	catalog, err := NewCatalogWithPolicy(policy, Binding{
		Descriptor: Descriptor{Name: "search", Parameters: ObjectSchema(nil, nil), Security: capability},
		Handler:    func(context.Context, json.RawMessage) (any, error) { return map[string]any{"ok": true}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor(catalog, ExecutorOptions{})
	missing := executor.Execute(context.Background(), ExecutionRequest{Tool: "search"})
	if missing.Error == nil || missing.Error.Code != ErrorCredentialScope {
		t.Fatalf("missing credential grant = %#v", missing.Error)
	}
	granted := executor.Execute(context.Background(), ExecutionRequest{Tool: "search", CredentialScopes: []string{"search_api"}})
	if granted.Error != nil {
		t.Fatalf("granted credential scope failed: %#v", granted.Error)
	}
}

func TestExecutorScopeResolverMayNarrowButNotWiden(t *testing.T) {
	readDocuments := toolpolicy.ResourceScope{Kind: toolpolicy.ResourceWorkspace, Name: "documents", Access: toolpolicy.AccessRead}
	readArchive := toolpolicy.ResourceScope{Kind: toolpolicy.ResourceWorkspace, Name: "archive", Access: toolpolicy.AccessRead}
	capability := toolpolicy.NormalizeCapability(toolpolicy.Capability{Scope: toolpolicy.Scope{Resources: []toolpolicy.ResourceScope{readDocuments}}})
	policy := policyFor("scoped_reader", toolpolicy.ActionAllow, capability)
	makeCatalog := func(resolver ScopeResolver) *Catalog {
		catalog, err := NewCatalogWithPolicy(policy, Binding{
			Descriptor:   Descriptor{Name: "scoped_reader", Parameters: ObjectSchema(nil, nil), Security: capability},
			ResolveScope: resolver,
			Handler:      func(context.Context, json.RawMessage) (any, error) { return map[string]any{"ok": true}, nil },
		})
		if err != nil {
			t.Fatal(err)
		}
		return catalog
	}

	allowed := NewExecutor(makeCatalog(func(context.Context, json.RawMessage) (toolpolicy.Scope, error) {
		return toolpolicy.Scope{Resources: []toolpolicy.ResourceScope{readDocuments}}, nil
	}), ExecutorOptions{}).Execute(context.Background(), ExecutionRequest{Tool: "scoped_reader"})
	if allowed.Error != nil {
		t.Fatalf("declared scope failed: %#v", allowed.Error)
	}

	denied := NewExecutor(makeCatalog(func(context.Context, json.RawMessage) (toolpolicy.Scope, error) {
		return toolpolicy.Scope{Resources: []toolpolicy.ResourceScope{readArchive}}, nil
	}), ExecutorOptions{}).Execute(context.Background(), ExecutionRequest{Tool: "scoped_reader"})
	if denied.Error == nil || denied.Error.Code != ErrorSecurityScopeInvalid || denied.PolicyDecision.Reason != "binding_scope_expansion" {
		t.Fatalf("scope expansion was not denied: %#v", denied)
	}
}

func TestExecutorScopeResolverFailuresAreTyped(t *testing.T) {
	tests := []struct {
		name     string
		resolver ScopeResolver
	}{
		{name: "error", resolver: func(context.Context, json.RawMessage) (toolpolicy.Scope, error) {
			return toolpolicy.Scope{}, errors.New("bad target")
		}},
		{name: "panic", resolver: func(context.Context, json.RawMessage) (toolpolicy.Scope, error) { panic("bad resolver") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog, err := NewCatalog(Binding{
				Descriptor: Descriptor{Name: "resolver", Parameters: ObjectSchema(nil, nil)}, ResolveScope: test.resolver,
				Handler: func(context.Context, json.RawMessage) (any, error) {
					t.Fatal("failed resolver reached handler")
					return nil, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			result := NewExecutor(catalog, ExecutorOptions{}).Execute(context.Background(), ExecutionRequest{Tool: "resolver"})
			if result.Error == nil || result.Error.Code != ErrorSecurityScopeInvalid || result.PolicyDecision.Reason != "scope_resolution_failed" {
				t.Fatalf("resolver failure = %#v", result)
			}
		})
	}
}

func TestExecutorAllowAndLogFailsClosedWithoutDurableAudit(t *testing.T) {
	capability := externalCapability(toolpolicy.Compensatable)
	policy := policyFor("audited_writer", toolpolicy.ActionAllowAndLog, capability)
	catalog, err := NewCatalogWithPolicy(policy, Binding{
		Descriptor: Descriptor{Name: "audited_writer", Parameters: ObjectSchema(nil, nil), Security: capability},
		Handler: func(context.Context, json.RawMessage) (any, error) {
			t.Fatal("missing audit reached handler")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result := NewExecutor(catalog, ExecutorOptions{}).Execute(context.Background(), ExecutionRequest{Tool: "audited_writer"})
	if result.Error == nil || result.Error.Code != ErrorSecurityAudit {
		t.Fatalf("missing audit was not fail closed: %#v", result.Error)
	}
	if result.Error.FailureInfo().Category != "availability" {
		t.Fatalf("security audit failure category = %#v", result.Error.FailureInfo())
	}

	result = NewExecutor(catalog, ExecutorOptions{Tracer: failingPolicyTracer{}}).Execute(context.Background(), ExecutionRequest{Tool: "audited_writer"})
	if result.Error == nil || result.Error.Code != ErrorSecurityAudit || !strings.Contains(result.Error.Unwrap().Error(), "audit unavailable") {
		t.Fatalf("audit failure was not preserved: %#v", result.Error)
	}
}

func TestExecutorApprovalActionsReturnTypedBlock(t *testing.T) {
	capability := toolpolicy.NormalizeCapability(toolpolicy.Capability{Approval: toolpolicy.ApprovalAsk})
	policy := policyFor("approval_tool", toolpolicy.ActionAsk, capability)
	catalog, err := NewCatalogWithPolicy(policy, Binding{
		Descriptor: Descriptor{Name: "approval_tool", Parameters: ObjectSchema(nil, nil), Security: capability},
		Handler:    func(context.Context, json.RawMessage) (any, error) { return nil, errors.New("must not execute") },
	})
	if err != nil {
		t.Fatal(err)
	}
	result := NewExecutor(catalog, ExecutorOptions{}).Execute(context.Background(), ExecutionRequest{Tool: "approval_tool"})
	if result.Error == nil || result.Error.Code != ErrorApprovalRequired {
		t.Fatalf("approval action = %#v", result.Error)
	}
}

func TestCatalogRejectsInvalidSecurityContracts(t *testing.T) {
	handler := func(context.Context, json.RawMessage) (any, error) { return nil, nil }
	tests := []struct {
		name       string
		descriptor Descriptor
	}{
		{
			name: "invalid capability",
			descriptor: Descriptor{
				Name: "invalid", Parameters: ObjectSchema(nil, nil),
				Security: toolpolicy.Capability{Source: "untrusted"},
			},
		},
		{
			name: "external effect without capability",
			descriptor: Descriptor{
				Name: "writer", Parameters: ObjectSchema(nil, nil),
				SideEffect: SideEffectPolicy{Mode: SideEffectExternal},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewCatalog(Binding{Descriptor: test.descriptor, Handler: handler}); err == nil {
				t.Fatal("invalid Tool security contract unexpectedly registered")
			}
		})
	}
}

func externalCapability(reversibility toolpolicy.Reversibility) toolpolicy.Capability {
	return toolpolicy.NormalizeCapability(toolpolicy.Capability{
		Scope:      toolpolicy.Scope{Resources: []toolpolicy.ResourceScope{{Kind: toolpolicy.ResourceExternal, Name: "records", Access: toolpolicy.AccessWrite}}},
		SideEffect: toolpolicy.SideEffectExternalWrite, Reversibility: reversibility,
		Visibility: toolpolicy.VisibilityOperator, Audit: toolpolicy.AuditFull,
	})
}

func policyFor(tool string, action toolpolicy.Action, capability toolpolicy.Capability) toolpolicy.Policy {
	return toolpolicy.Policy{Version: "operator-test-v1", DefaultAction: toolpolicy.ActionDeny, Rules: []toolpolicy.Rule{{
		ID: "rule-" + tool, Tool: tool, Action: action, Capability: capability,
	}}}
}

type failingPolicyTracer struct{}

func (failingPolicyTracer) ToolStarted(context.Context, ExecutionRequest) {}
func (failingPolicyTracer) ToolFinished(context.Context, ExecutionResult) {}
func (failingPolicyTracer) ToolPolicyEvaluated(context.Context, ExecutionRequest, toolpolicy.Decision) error {
	return errors.New("audit unavailable")
}
