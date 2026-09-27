package tool

import (
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/tool/policy"
)

func BuildCatalog(config Config, extra ...Binding) (*Catalog, error) {
	securityPolicy := config.SecurityPolicy
	if securityPolicy.Version == "" && securityPolicy.DefaultAction == "" && len(securityPolicy.Rules) == 0 {
		securityPolicy = policy.DefaultPolicy()
	}
	builtins := builtinTools()
	bindings := make([]Binding, 0, len(builtins)+len(extra))
	provided := make(map[string]bool, len(extra))
	for _, binding := range extra {
		provided[binding.Descriptor.Name] = true
	}
	for _, builtin := range builtins {
		if !builtin.runtimeInjectable || !provided[builtin.binding.Descriptor.Name] {
			bindings = append(bindings, builtin.binding)
		}
	}
	bindings = append(bindings, extra...)
	catalog, err := NewCatalogWithPolicy(securityPolicy, bindings...)
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
