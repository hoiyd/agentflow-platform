package tool

import "agentflow-platform/apps/api/internal/tool/policy"

func newExternalTestCatalog(binding Binding) (*Catalog, error) {
	binding.Descriptor.Security = policy.NormalizeCapability(policy.Capability{
		Scope: policy.Scope{Resources: []policy.ResourceScope{{
			Kind: policy.ResourceExternal, Name: "test_fixture", Access: policy.AccessWrite,
		}}},
		SideEffect: policy.SideEffectExternalWrite, Reversibility: policy.Compensatable,
		Visibility: policy.VisibilityOperator, Audit: policy.AuditBasic,
	})
	securityPolicy := policy.DefaultPolicy()
	securityPolicy.Rules = append(securityPolicy.Rules, policy.Rule{
		ID: "test-" + binding.Descriptor.Name, Tool: binding.Descriptor.Name,
		Action: policy.ActionAllow, Capability: binding.Descriptor.Security,
	})
	return NewCatalogWithPolicy(securityPolicy, binding)
}
