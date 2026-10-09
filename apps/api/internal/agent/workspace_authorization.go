package agent

import (
	"context"
	"slices"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/skill"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/availability"
	"agentflow-platform/apps/api/internal/tool/policy"
)

// Catalog includes runtime-owned bindings as well as operator-installed ones.
// HTTP and runtime use this one catalog to explain and enforce availability.
func (r *Runtime) Catalog() (*tool.Catalog, error) { return r.currentCatalog() }

func (r *Runtime) currentRunAgent(run domain.Run, id string) (domain.Agent, error) {
	if err := r.store.CheckWorkspaceExecution(run.WorkspaceID); err != nil {
		return domain.Agent{}, err
	}
	item, ok, err := r.store.GetAgentInWorkspace(run.WorkspaceID, id)
	if err != nil {
		return domain.Agent{}, err
	}
	if !ok || item.Archived {
		return domain.Agent{}, store.ErrNotFound("agent")
	}
	return item, nil
}

func (r *Runtime) authorizeTool(runID string, agent domain.Agent) func(context.Context, tool.ExecutionRequest, policy.Scope) error {
	return func(_ context.Context, call tool.ExecutionRequest, scope policy.Scope) error {
		if call.RunID != runID || !slices.Contains(agent.Tools, call.Tool) {
			return &availability.DeniedError{Reason: "agent_disabled"}
		}
		run, ok, err := r.store.GetRun(runID)
		if err != nil {
			return err
		}
		if !ok {
			return store.ErrNotFound("run")
		}
		currentAgent, err := r.currentRunAgent(run, agent.ID)
		if err != nil {
			return err
		}
		currentTools := r.withHarnessTools(currentAgent.Tools)
		if len(currentAgent.Skills) > 0 {
			currentTools = append(currentTools, skill.LoadToolName, skill.ReadToolName)
		}
		if !slices.Contains(currentTools, call.Tool) {
			return &availability.DeniedError{Reason: "agent_disabled"}
		}
		catalog, err := r.currentCatalog()
		if err != nil {
			return err
		}
		catalog, items, err := availability.Resolve(r.store, run.WorkspaceID, catalog)
		if err != nil {
			return err
		}
		for _, item := range items {
			if item.Name == call.Tool && !item.Enabled {
				return &availability.DeniedError{Reason: item.ExcludedReason, Revision: item.ConfigRevision}
			}
		}
		if err = availability.RequireTool(catalog, call.Tool); err != nil {
			return err
		}
		binding, _ := catalog.ResolveReady(call.Tool)
		decision := policy.Evaluate(catalog.SecurityPolicy(), policy.Request{Tool: call.Tool, Declared: binding.Descriptor.Security, RequestedScope: scope, AvailableCredentialScopes: call.CredentialScopes})
		if !decision.Allowed {
			return &availability.DeniedError{Reason: "policy_denied"}
		}
		return nil
	}
}
