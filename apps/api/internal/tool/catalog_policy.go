package tool

import (
	"agentflow-platform/apps/api/internal/tool/policy"
)

func (c *Catalog) WorkspaceMCPCredentialScopes() []string {
	var scopes []string
	for _, item := range c.List() {
		if item.Source == nil || item.Source.Kind != "mcp" || !mcpWorkspaceServerID.MatchString(item.Source.ServerID) {
			continue
		}
		binding, ok := c.ResolveReady(item.Name)
		if ok {
			scopes = append(scopes, binding.Descriptor.Security.Scope.Credentials...)
		}
	}
	return scopes
}

// WithRules adds locally authorized Workspace rules without overriding an
// explicit operator rule (including deny) for the same tool.
func (c *Catalog) WithRules(rules []policy.Rule) (*Catalog, error) {
	clone, err := c.CloneWith()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, rule := range clone.securityPolicy.Rules {
		seen[rule.Tool] = true
	}
	for _, rule := range rules {
		if !seen[rule.Tool] {
			clone.securityPolicy.Rules = append(clone.securityPolicy.Rules, rule)
			seen[rule.Tool] = true
		}
	}
	clone.securityPolicy = policy.NormalizePolicy(clone.securityPolicy)
	if err := policy.ValidatePolicy(clone.securityPolicy); err != nil {
		return nil, err
	}
	return clone, nil
}
