package agent

import (
	"agentflow-platform/apps/api/internal/clarification"
	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/skill"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/availability"
	"agentflow-platform/apps/api/internal/tool/progress"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"
)

func (r *Runtime) captureRuntimeSnapshot(mode string, agent domain.Agent, candidates []domain.Agent) (domain.RuntimeSnapshot, error) {
	catalog, err := r.currentCatalog(agent.WorkspaceID)
	if err != nil {
		return domain.RuntimeSnapshot{}, err
	}
	agent = domain.NormalizeAgentConfig(agent)
	if agent.WorkspaceID != "" {
		catalog, _, err = availability.Resolve(r.store, agent.WorkspaceID, catalog)
		if err != nil {
			return domain.RuntimeSnapshot{}, err
		}
	}
	toolCallingAvailable := false
	if r.modelRoutes != nil {
		for _, route := range r.modelRoutes.Descriptors() {
			toolCallingAvailable = toolCallingAvailable || route.Capabilities.ToolCalling
		}
	}
	if toolCallingAvailable {
		agent.Tools = r.withHarnessTools(agent.Tools)
	}
	skillSnapshots := []domain.SkillSnapshot{}
	seenSkills := map[string]bool{}
	freezeSkills := func(agent *domain.Agent) error {
		if err := domain.ValidateSkillPolicies(agent.Skills, agent.SkillPolicies); err != nil {
			return &tool.ExecutionError{Code: "skill_invocation_invalid", Message: err.Error()}
		}
		if len(agent.Skills) == 0 {
			return nil
		}
		packages, err := r.freezeAgentSkills(*agent, catalog)
		if err != nil {
			return err
		}
		for _, item := range packages {
			if !seenSkills[item.Name] {
				seenSkills[item.Name] = true
				skillSnapshots = append(skillSnapshots, item)
			}
		}
		if toolCallingAvailable {
			agent.Tools = append(agent.Tools, skill.LoadToolName, skill.ReadToolName)
		}
		return nil
	}
	if err := freezeSkills(&agent); err != nil {
		return domain.RuntimeSnapshot{}, err
	}
	agent = availability.FilterAgent(agent, catalog)
	candidateSnapshots := make([]domain.RuntimeAgentSnapshot, 0, len(candidates))
	toolNames := append([]string(nil), agent.Tools...)
	for _, candidate := range candidates {
		candidate = domain.NormalizeAgentConfig(candidate)
		if toolCallingAvailable {
			candidate.Tools = r.withHarnessTools(candidate.Tools)
			// Clarification is a parent boundary, never an isolated Worker capability.
			candidate.Tools = slices.DeleteFunc(candidate.Tools, func(name string) bool { return name == clarification.ToolName })
		}
		if err := freezeSkills(&candidate); err != nil {
			return domain.RuntimeSnapshot{}, err
		}
		candidate = availability.FilterAgent(candidate, catalog)
		candidateSnapshots = append(candidateSnapshots, snapshotAgent(candidate))
		toolNames = append(toolNames, candidate.Tools...)
	}
	toolSnapshots := snapshotTools(catalog, toolNames)
	modelRouting, err := r.captureModelRoutingSnapshot()
	if err != nil {
		return domain.RuntimeSnapshot{}, err
	}
	if r.embeddingClient == nil {
		return domain.RuntimeSnapshot{}, errors.New("embedding client is required")
	}
	identity := r.embeddingClient.RuntimeIdentity()
	runBudget := r.runBudget
	toolSchema := r.toolSchemaConfig
	snapshot := domain.RuntimeSnapshot{
		SchemaVersion:   domain.CurrentRuntimeSnapshotVersion,
		Mode:            mode,
		Agent:           snapshotAgent(agent),
		CandidateAgents: candidateSnapshots,
		Embedding: domain.RuntimeEmbeddingSnapshot{
			Provider: identity.EmbeddingProvider, BaseURL: identity.EmbeddingBaseURL,
			Model: identity.EmbeddingModel, Dimensions: identity.EmbeddingDimensions,
		},
		ModelRouting:       modelRouting,
		Tools:              toolSnapshots,
		Skills:             skillSnapshots,
		ToolSecurityPolicy: catalog.SecurityPolicy(),
		ToolProgressGuard:  progress.NormalizeConfig(r.toolProgressConfig),
		ToolSchema:         &toolSchema,
		ContextAssembly:    contextassembly.NormalizeConfig(r.contextAssemblyConfig),
		RouterMode:         r.routerMode,
		RunBudget:          cloneRunBudget(runBudget),
		CreatedAt:          time.Now().UTC(),
	}
	if mode == ChatModeAutonomous {
		limits := normalizeAutonomousLimits(r.autonomousLimits)
		runBudget = effectiveAutonomousRunBudget(runBudget, limits)
		snapshot.RunBudget = cloneRunBudget(runBudget)
		snapshot.AutonomousLimits = &domain.RuntimeLimitsSnapshot{
			MaxIterations:  limits.MaxIterations,
			MaxOutputChars: limits.MaxOutputChars,
		}
	}
	return snapshot, nil
}

// ValidateAgentSkills checks dependencies without installing or granting Tools.
// The actual Run captures the same packages after harness Tool availability is known.
func (r *Runtime) ValidateAgentSkills(agent domain.Agent) error {
	agent = domain.NormalizeAgentConfig(agent)
	if err := domain.ValidateSkillPolicies(agent.Skills, agent.SkillPolicies); err != nil {
		return &tool.ExecutionError{Code: "skill_invocation_invalid", Message: err.Error()}
	}
	catalog, err := r.currentCatalog(agent.WorkspaceID)
	if err != nil {
		return err
	}
	if agent.WorkspaceID != "" {
		catalog, _, err = availability.Resolve(r.store, agent.WorkspaceID, catalog)
		if err != nil {
			return err
		}
	}
	agent.Tools = r.withHarnessTools(agent.Tools)
	_, err = r.freezeAgentSkills(agent, catalog)
	return err
}

func (r *Runtime) freezeAgentSkills(agent domain.Agent, catalog *tool.Catalog) ([]domain.SkillSnapshot, error) {
	ready := []string{}
	for _, name := range agent.Tools {
		if _, ok := catalog.ResolveReady(name); ok {
			ready = append(ready, name)
		}
	}
	return r.skills.Freeze(agent.Skills, ready)
}

// ListSkills explains current Workspace readiness; it does not grant Agent
// bindings or promise that argument-specific Executor authorization will pass.
func (r *Runtime) ListSkills(workspaceID string) ([]domain.SkillMetadata, error) {
	items := r.skills.List()
	if len(items) == 0 {
		return items, nil
	}
	catalog, err := r.currentCatalog(workspaceID)
	if err != nil {
		return nil, err
	}
	_, tools, err := availability.Resolve(r.store, workspaceID, catalog)
	if err != nil {
		return nil, err
	}
	harness := r.withHarnessTools(nil)
	for i := range items {
		for _, name := range items[i].RequiredTools {
			dependency := domain.SkillToolDependency{Name: name, Reason: "not_installed", AgentBindingRequired: !slices.Contains(harness, name)}
			for _, item := range tools {
				if item.Name == name {
					dependency.Ready, dependency.Reason = item.Enabled, item.ExcludedReason
					break
				}
			}
			items[i].ToolDependencies = append(items[i].ToolDependencies, dependency)
		}
	}
	return items, nil
}

func snapshotAgent(agent domain.Agent) domain.RuntimeAgentSnapshot {
	return domain.RuntimeAgentSnapshot{
		ID: agent.ID, Name: agent.Name, Description: agent.Description, SystemPrompt: agent.SystemPrompt,
		RoutingHints: cloneAgentRoutingHints(agent.RoutingHints),
		Tools:        append([]string(nil), agent.Tools...), MemoryEnabled: agent.MemoryEnabled,
		Skills:           append([]string(nil), agent.Skills...),
		SkillPolicies:    domain.NormalizeAgentConfig(agent).SkillPolicies,
		RetrievalEnabled: agent.RetrievalEnabled, Executor: domain.DefaultAgentExecutor,
	}
}

func cloneAgentRoutingHints(hints domain.AgentRoutingHints) domain.AgentRoutingHints {
	return domain.AgentRoutingHints{
		Capabilities: append([]string(nil), hints.Capabilities...),
		TaskExamples: append([]string(nil), hints.TaskExamples...),
		Exclusions:   append([]string(nil), hints.Exclusions...),
	}
}

func snapshotTools(catalog *tool.Catalog, names []string) []domain.RuntimeToolSnapshot {
	seen := map[string]bool{}
	items := make([]domain.RuntimeToolSnapshot, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		binding, ok := catalog.ResolveReady(name)
		if !ok {
			continue
		}
		items = append(items, domain.RuntimeToolSnapshot{
			Name: binding.Descriptor.Name, Description: binding.Descriptor.Description,
			Parameters:         binding.Descriptor.Parameters,
			SchemaVersion:      binding.Descriptor.SchemaVersion,
			DefinitionRevision: binding.Descriptor.DefinitionRevision,
			SideEffect:         string(binding.Descriptor.JournalMode()),
			Security:           binding.Descriptor.Security,
			Source:             binding.Descriptor.Source,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

func cloneRunBudget(value domain.RuntimeRunBudget) *domain.RuntimeRunBudget {
	cloned := value
	return &cloned
}

// effectiveAutonomousRunBudget resolves the two configuration profiles once at
// Run creation. Runtime and tool usage then have one enforcement owner: Run Budget.
func effectiveAutonomousRunBudget(runBudget domain.RuntimeRunBudget, limits AutonomousLimits) domain.RuntimeRunBudget {
	limits = normalizeAutonomousLimits(limits)
	modeRuntimeMS := limits.MaxRuntime.Milliseconds()
	if modeRuntimeMS > 0 && (runBudget.MaxRuntimeMS <= 0 || modeRuntimeMS < runBudget.MaxRuntimeMS) {
		runBudget.MaxRuntimeMS = modeRuntimeMS
	}
	if limits.MaxToolCalls > 0 && (runBudget.MaxToolCalls <= 0 || limits.MaxToolCalls < runBudget.MaxToolCalls) {
		runBudget.MaxToolCalls = limits.MaxToolCalls
	}
	return runBudget
}
