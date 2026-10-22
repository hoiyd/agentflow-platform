package agent

import (
	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/availability"
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"strings"
)

type restoredRuntime struct {
	mode            string
	agent           domain.Agent
	candidateAgents []domain.Agent
	catalog         *tool.Catalog
	modelConfigured bool
	routerMode      string
}

func restoreAgent(snapshot domain.RuntimeAgentSnapshot) domain.Agent {
	return domain.Agent{
		ID:               snapshot.ID,
		Name:             snapshot.Name,
		Description:      snapshot.Description,
		SystemPrompt:     snapshot.SystemPrompt,
		RoutingHints:     cloneAgentRoutingHints(snapshot.RoutingHints),
		Tools:            append([]string(nil), snapshot.Tools...),
		Skills:           append([]string(nil), snapshot.Skills...),
		SkillPolicies:    maps.Clone(snapshot.SkillPolicies),
		MemoryEnabled:    snapshot.MemoryEnabled,
		RetrievalEnabled: snapshot.RetrievalEnabled,
		Executor:         domain.DefaultAgentExecutor,
	}
}

func (r *Runtime) restoreRuntime(run domain.Run) (restoredRuntime, error) {
	if err := validateRuntimeSnapshot(run.RuntimeSnapshot); err != nil {
		return restoredRuntime{}, fmt.Errorf("run %s cannot be resumed safely: %w", run.ID, err)
	}
	snapshot := run.RuntimeSnapshot
	if err := r.validateKnowledgeEvidence(run); err != nil {
		return restoredRuntime{}, err
	}
	if _, err := r.currentRunAgent(run, snapshot.Agent.ID); err != nil {
		return restoredRuntime{}, err
	}
	for _, candidate := range snapshot.CandidateAgents {
		if _, err := r.currentRunAgent(run, candidate.ID); err != nil {
			return restoredRuntime{}, err
		}
	}
	snapshot.ContextAssembly = contextassembly.NormalizeConfig(snapshot.ContextAssembly)
	current, err := r.currentCatalog()
	if err != nil {
		return restoredRuntime{}, err
	}
	current, _, err = availability.Resolve(r.store, run.WorkspaceID, current)
	if err != nil {
		return restoredRuntime{}, err
	}
	restoredBindings := make([]tool.Binding, 0, len(snapshot.Tools))
	for _, frozen := range snapshot.Tools {
		installed, ok := current.Installed(frozen.Name)
		if !ok {
			return restoredRuntime{}, fmt.Errorf("%w: frozen tool %q is no longer installed", ErrRuntimeSnapshotUnavailable, frozen.Name)
		}
		if !toolDefinitionMatches(installed, frozen) {
			return restoredRuntime{}, fmt.Errorf("%w: frozen tool %q no longer matches its captured definition", ErrRuntimeSnapshotUnavailable, frozen.Name)
		}
		if installed.UnavailableReason != "" {
			return restoredRuntime{}, fmt.Errorf("%w: frozen tool %q is unavailable: %s", ErrRuntimeSnapshotUnavailable, frozen.Name, installed.UnavailableReason)
		}
		restoredBindings = append(restoredBindings, installed)
	}
	catalog, err := tool.NewCatalogWithPolicy(snapshot.ToolSecurityPolicy, restoredBindings...)
	if err != nil {
		return restoredRuntime{}, err
	}
	for _, frozen := range snapshot.Tools {
		_, ready := current.ResolveReady(frozen.Name)
		if err := catalog.SetEnabled(frozen.Name, ready); err != nil {
			return restoredRuntime{}, err
		}
	}
	modelRoutes, err := r.restoreModelRouteCatalog(snapshot.ModelRouting)
	if err != nil {
		return restoredRuntime{}, err
	}
	candidates := make([]domain.Agent, 0, len(snapshot.CandidateAgents))
	for _, candidate := range snapshot.CandidateAgents {
		candidates = append(candidates, restoreAgent(candidate))
	}
	return restoredRuntime{
		mode: snapshot.Mode, agent: restoreAgent(snapshot.Agent), candidateAgents: candidates,
		catalog: catalog, modelConfigured: modelRoutes.HasConfiguredClient(), routerMode: NormalizeRouterMode(snapshot.RouterMode),
	}, nil
}

func toolDefinitionMatches(installed tool.Binding, frozen domain.RuntimeToolSnapshot) bool {
	if frozen.SchemaVersion != tool.ToolSchemaVersion || strings.TrimSpace(frozen.DefinitionRevision) == "" {
		return false
	}
	current := domain.RuntimeToolSnapshot{
		Name: installed.Descriptor.Name, Description: installed.Descriptor.Description,
		Parameters: installed.Descriptor.Parameters, SideEffect: string(installed.Descriptor.JournalMode()),
		SchemaVersion: installed.Descriptor.SchemaVersion, DefinitionRevision: installed.Descriptor.DefinitionRevision,
		Security: installed.Descriptor.Security,
		Source:   installed.Descriptor.Source,
	}
	currentJSON, err := json.Marshal(current)
	if err != nil {
		return false
	}
	frozenJSON, err := json.Marshal(frozen)
	return err == nil && bytes.Equal(currentJSON, frozenJSON)
}
