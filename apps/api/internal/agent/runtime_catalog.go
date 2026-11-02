package agent

import (
	"slices"

	"agentflow-platform/apps/api/internal/clarification"
	"agentflow-platform/apps/api/internal/tool"
)

// Harness definitions are validated once; each request still reloads operator
// configuration and applies the current Workspace grants.
func (r *Runtime) compileHarnessBindings() error {
	bindings := make([]tool.Binding, 0, 3)
	automaticNames := []string{}
	register := func(autoInclude bool, items ...tool.Binding) {
		bindings = append(bindings, items...)
		if autoInclude {
			for _, binding := range items {
				automaticNames = append(automaticNames, binding.Descriptor.Name)
			}
		}
	}
	if r.clarifications != nil {
		register(true, clarification.Binding())
	}
	if r.taskStates != nil {
		register(true, r.taskStates.ToolBinding())
	}
	if r.toolArtifacts != nil {
		register(true, r.toolArtifacts.ToolBindings()...)
	}
	register(true, r.knowledgeTools...)
	// Installed Skill bindings become eligible only for Agents with frozen Skills.
	register(false, r.skillService.ToolBindings()...)
	catalog, err := tool.NewCatalog(bindings...)
	if err != nil {
		return err
	}
	compiled := make([]tool.Binding, 0, len(bindings))
	for _, item := range catalog.List() {
		binding, _ := catalog.Installed(item.Name)
		compiled = append(compiled, binding)
	}
	r.harnessBindings, r.automaticHarnessTools = compiled, automaticNames
	return nil
}

func (r *Runtime) currentCatalog(workspace string) (*tool.Catalog, error) {
	catalog, err := r.tools.CatalogForWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	return catalog.CloneWith(r.harnessBindings...)
}

func (r *Runtime) withHarnessTools(names []string) []string {
	result := append([]string(nil), names...)
	for _, harnessName := range r.automaticHarnessTools {
		if !slices.Contains(result, harnessName) {
			result = append(result, harnessName)
		}
	}
	return result
}
