package tool

import (
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/tool/policy"
)

func BuildCatalog(config Config) (*Catalog, error) {
	securityPolicy := config.SecurityPolicy
	if securityPolicy.Version == "" && securityPolicy.DefaultAction == "" && len(securityPolicy.Rules) == 0 {
		securityPolicy = policy.DefaultPolicy()
	}
	catalog, err := NewCatalogWithPolicy(securityPolicy, CalculatorTool(), CurrentTimeTool())
	if err != nil {
		return nil, err
	}
	enabled := make(map[string]bool, len(config.EnabledTools))
	for _, name := range config.EnabledTools {
		if name = strings.TrimSpace(name); name != "" {
			enabled[name] = true
		}
	}
	installed := make(map[string]bool)
	for _, item := range catalog.List() {
		installed[item.Name] = true
		if err := catalog.SetEnabled(item.Name, enabled[item.Name]); err != nil {
			return nil, err
		}
	}
	for name := range enabled {
		if !installed[name] {
			return nil, fmt.Errorf("enabled tool %q is not installed", name)
		}
	}
	return catalog, nil
}
