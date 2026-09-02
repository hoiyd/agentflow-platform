package tools

import "agentflow-platform/apps/api/internal/toolpolicy"

func newExternalTestCatalog(binding Binding) (*Catalog, error) {
	binding.Descriptor.Security = toolpolicy.NormalizeCapability(toolpolicy.Capability{
		Scope: toolpolicy.Scope{Resources: []toolpolicy.ResourceScope{{
			Kind: toolpolicy.ResourceExternal, Name: "test_fixture", Access: toolpolicy.AccessWrite,
		}}},
		SideEffect: toolpolicy.SideEffectExternalWrite, Reversibility: toolpolicy.Compensatable,
		Visibility: toolpolicy.VisibilityOperator, Audit: toolpolicy.AuditBasic,
	})
	policy := toolpolicy.DefaultPolicy()
	policy.Rules = append(policy.Rules, toolpolicy.Rule{
		ID: "test-" + binding.Descriptor.Name, Tool: binding.Descriptor.Name,
		Action: toolpolicy.ActionAllow, Capability: binding.Descriptor.Security,
	})
	return NewCatalogWithPolicy(policy, binding)
}
