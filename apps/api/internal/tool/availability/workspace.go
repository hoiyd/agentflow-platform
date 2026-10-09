// Package availability computes Workspace Tool grants. It does not install
// bindings, supply credentials, or replace the Executor's security policy.
package availability

import (
	"fmt"
	"slices"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
)

func Resolve(storage store.WorkspaceToolStore, workspaceID string, service *tool.Catalog) (*tool.Catalog, []tool.ToolInfo, error) {
	config, err := storage.EnsureWorkspaceToolConfig(workspaceID, service.EnabledNames())
	if err != nil {
		return nil, nil, err
	}
	catalog, err := service.CloneWith()
	if err != nil {
		return nil, nil, err
	}
	items := service.List()
	for i := range items {
		item := &items[i]
		item.ServiceEnabled = item.Enabled
		item.WorkspaceEnabled = slices.Contains(config.AllowedTools, item.Name)
		item.ConfigRevision = config.Revision
		switch {
		case !item.ServiceEnabled:
			item.ExcludedReason = "service_disabled"
		case item.UnavailableReason != "":
			item.ExcludedReason = item.UnavailableReason
		case !item.WorkspaceEnabled:
			item.ExcludedReason = "workspace_disabled"
		}
		if item.ExcludedReason == "" {
			binding, _ := service.Installed(item.Name)
			decision := policy.Evaluate(service.SecurityPolicy(), policy.Request{Tool: item.Name, Declared: binding.Descriptor.Security, RequestedScope: binding.Descriptor.Security.Scope, AvailableCredentialScopes: binding.Descriptor.Security.Scope.Credentials})
			if !decision.Allowed {
				item.ExcludedReason = "policy_denied"
			}
		}
		item.Enabled = item.ExcludedReason == ""
		if err := catalog.SetEnabled(item.Name, item.Enabled); err != nil {
			return nil, nil, err
		}
	}
	return catalog, items, nil
}

type DeniedError struct {
	Reason   string
	Revision int64
}

func (e *DeniedError) Error() string {
	return fmt.Sprintf("Tool availability denied: %s (Workspace config revision %d)", e.Reason, e.Revision)
}
func (e *DeniedError) AvailabilityReason() string { return e.Reason }

func RequireTool(catalog *tool.Catalog, name string) error {
	if _, ok := catalog.ResolveReady(name); !ok {
		return fmt.Errorf("tool %q is not available in this Workspace", name)
	}
	return nil
}

// FilterAgent retains protocol configuration separately from effective grants.
// Harness Tools are added by Runtime before this same intersection is applied.
func FilterAgent(agent domain.Agent, catalog *tool.Catalog) domain.Agent {
	names := []string{}
	for _, name := range agent.Tools {
		if _, ok := catalog.ResolveReady(name); ok {
			names = append(names, name)
		}
	}
	agent.Tools = names
	return agent
}
