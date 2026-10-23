package agent

import (
	"agentflow-platform/apps/api/internal/clarification"
	"agentflow-platform/apps/api/internal/taskstate"
	"agentflow-platform/apps/api/internal/tool"
)

// Harness definitions are validated once; each request still reloads operator
// configuration and applies the current Workspace grants.
func (r *Runtime) compileHarnessBindings() error {
	bindings := make([]tool.Binding, 0, 3)
	if r.clarifications != nil {
		bindings = append(bindings, clarification.Binding())
	}
	if r.taskStates != nil {
		bindings = append(bindings, r.taskStates.ToolBinding())
	}
	if r.toolArtifacts != nil {
		bindings = append(bindings, r.toolArtifacts.ToolBindings()...)
	}
	bindings = append(bindings, r.knowledgeTools...)
	bindings = append(bindings, r.skillService.ToolBindings()...)
	catalog, err := tool.NewCatalog(bindings...)
	if err != nil {
		return err
	}
	for _, item := range catalog.List() {
		binding, _ := catalog.Installed(item.Name)
		r.harnessBindings = append(r.harnessBindings, binding)
	}
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
	harnessNames := make([]string, 0, 3)
	if r.clarifications != nil {
		harnessNames = append(harnessNames, clarification.ToolName)
	}
	if r.taskStates != nil {
		harnessNames = append(harnessNames, taskstate.UpdateToolName)
	}
	if r.toolArtifacts != nil {
		harnessNames = append(harnessNames, r.toolArtifacts.ToolNames()...)
	}
	for _, binding := range r.knowledgeTools {
		harnessNames = append(harnessNames, binding.Descriptor.Name)
	}
	for _, harnessName := range harnessNames {
		found := false
		for _, name := range result {
			if name == harnessName {
				found = true
				break
			}
		}
		if !found {
			result = append(result, harnessName)
		}
	}
	return result
}
