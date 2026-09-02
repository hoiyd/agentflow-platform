package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/modelprovider"
	"agentflow-platform/apps/api/internal/taskstate"
	"agentflow-platform/apps/api/internal/toolpolicy"
	"agentflow-platform/apps/api/internal/toolprogress"
	"agentflow-platform/apps/api/internal/tools"
)

var ErrRuntimeSnapshotUnavailable = failure.New(failure.Definition{
	Message: "runtime snapshot is unavailable",
	Info: failure.Info{
		Code: "runtime_snapshot_unavailable", Source: "runtime_snapshot",
		Category: failure.CategoryAvailability, Retryable: false,
	},
})

var ErrRuntimeSnapshotResumeUnsupported = failure.New(failure.Definition{
	Message: "runtime snapshot version is not supported for resume",
	Info: failure.Info{
		Code: "runtime_snapshot_resume_unsupported", Source: "runtime_snapshot",
		Category: failure.CategoryValidation, Retryable: false,
	},
})

var ErrRuntimeExecutorUnsupported = failure.New(failure.Definition{
	Message: "runtime executor protocol is no longer supported",
	Info: failure.Info{
		Code: "runtime_executor_unsupported", Source: "runtime_snapshot",
		Category: failure.CategoryAvailability, Retryable: false,
	},
})

type restoredRuntime struct {
	mode            string
	agent           domain.Agent
	candidateAgents []domain.Agent
	catalog         *tools.Catalog
	client          modelprovider.Client
	routerMode      string
}

func (r *Runtime) captureRuntimeSnapshot(mode string, agent domain.Agent, candidates []domain.Agent) (domain.RuntimeSnapshot, error) {
	catalog, err := r.currentCatalog()
	if err != nil {
		return domain.RuntimeSnapshot{}, err
	}
	agent = domain.NormalizeAgentConfig(agent)
	agent.Tools = r.withHarnessTools(agent.Tools)
	candidateSnapshots := make([]domain.RuntimeAgentSnapshot, 0, len(candidates))
	toolNames := append([]string(nil), agent.Tools...)
	for _, candidate := range candidates {
		candidate = domain.NormalizeAgentConfig(candidate)
		candidate.Tools = r.withHarnessTools(candidate.Tools)
		candidateSnapshots = append(candidateSnapshots, snapshotAgent(candidate))
		toolNames = append(toolNames, candidate.Tools...)
	}
	toolSnapshots := snapshotTools(catalog, toolNames)
	identity := r.modelClient.RuntimeIdentity()
	runBudget := r.runBudget
	snapshot := domain.RuntimeSnapshot{
		SchemaVersion:   domain.CurrentRuntimeSnapshotVersion,
		Mode:            mode,
		Agent:           snapshotAgent(agent),
		CandidateAgents: candidateSnapshots,
		Model: domain.RuntimeModelSnapshot{
			Provider: identity.Provider, BaseURL: identity.BaseURL, Model: identity.Model,
			EmbeddingBaseURL: identity.EmbeddingBaseURL, EmbeddingModel: identity.EmbeddingModel,
			EmbeddingDimensions: identity.EmbeddingDimensions,
		},
		Tools:              toolSnapshots,
		ToolSecurityPolicy: catalog.SecurityPolicy(),
		ToolProgressGuard:  toolprogress.NormalizeConfig(r.toolProgressConfig),
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
	if mode == ChatModeMultiAgent {
		limits := normalizeChildRunLimits(r.childRunLimits)
		snapshot.ChildRunPolicy = &domain.RuntimeChildRunPolicy{
			MaxDepth: 1, TimeoutMS: limits.Timeout.Milliseconds(),
			SummaryMaxChars:       limits.SummaryMaxChars,
			AgentDefinitionSource: "runtime_snapshot.candidate_agents",
			RunBudget:             limits.RunBudget,
		}
	}
	return snapshot, nil
}

func (r *Runtime) currentCatalog() (*tools.Catalog, error) {
	var catalog *tools.Catalog
	var err error
	if r.tools == nil {
		catalog = tools.DefaultCatalog()
	} else {
		catalog, err = r.tools.Catalog()
		if err != nil {
			return nil, err
		}
	}
	bindings := make([]tools.Binding, 0, 3)
	if r.taskStates != nil {
		bindings = append(bindings, r.taskStates.ToolBinding())
	}
	if r.toolArtifacts != nil {
		bindings = append(bindings, r.toolArtifacts.ToolBindings()...)
	}
	if len(bindings) == 0 {
		return catalog, nil
	}
	return catalog.CloneWith(bindings...)
}

func (r *Runtime) withHarnessTools(names []string) []string {
	result := append([]string(nil), names...)
	harnessNames := make([]string, 0, 3)
	if r.taskStates != nil {
		harnessNames = append(harnessNames, taskstate.UpdateToolName)
	}
	if r.toolArtifacts != nil {
		harnessNames = append(harnessNames, r.toolArtifacts.ToolNames()...)
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

func snapshotAgent(agent domain.Agent) domain.RuntimeAgentSnapshot {
	return domain.RuntimeAgentSnapshot{
		ID: agent.ID, Name: agent.Name, Description: agent.Description, SystemPrompt: agent.SystemPrompt,
		Tools: append([]string(nil), agent.Tools...), MemoryEnabled: agent.MemoryEnabled,
		RetrievalEnabled: agent.RetrievalEnabled, Executor: domain.DefaultAgentExecutor,
	}
}

func restoreAgent(snapshot domain.RuntimeAgentSnapshot) domain.Agent {
	return domain.Agent{
		ID:               snapshot.ID,
		Name:             snapshot.Name,
		Description:      snapshot.Description,
		SystemPrompt:     snapshot.SystemPrompt,
		Tools:            append([]string(nil), snapshot.Tools...),
		MemoryEnabled:    snapshot.MemoryEnabled,
		RetrievalEnabled: snapshot.RetrievalEnabled,
		Executor:         domain.DefaultAgentExecutor,
	}
}

func snapshotTools(catalog *tools.Catalog, names []string) []domain.RuntimeToolSnapshot {
	seen := map[string]bool{}
	items := make([]domain.RuntimeToolSnapshot, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		binding, ok := catalog.Resolve(name)
		if !ok {
			continue
		}
		items = append(items, domain.RuntimeToolSnapshot{
			Name: binding.Descriptor.Name, Description: binding.Descriptor.Description,
			Parameters:         binding.Descriptor.Parameters,
			SchemaVersion:      binding.Descriptor.SchemaVersion,
			DefinitionRevision: binding.Descriptor.DefinitionRevision,
			SideEffect:         string(binding.Descriptor.SideEffect.Mode),
			Security:           binding.Descriptor.Security,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

func (r *Runtime) restoreRuntime(run domain.Run) (restoredRuntime, error) {
	if err := validateRuntimeSnapshot(run.RuntimeSnapshot); err != nil {
		return restoredRuntime{}, fmt.Errorf("run %s cannot be resumed safely: %w", run.ID, err)
	}
	snapshot := run.RuntimeSnapshot
	snapshot.ContextAssembly = contextassembly.NormalizeConfig(snapshot.ContextAssembly)
	current, err := r.currentCatalog()
	if err != nil {
		return restoredRuntime{}, err
	}
	restoredBindings := make([]tools.Binding, 0, len(snapshot.Tools))
	for _, frozen := range snapshot.Tools {
		installed, ok := current.Installed(frozen.Name)
		if !ok {
			return restoredRuntime{}, fmt.Errorf("frozen tool %q is no longer installed", frozen.Name)
		}
		if !toolDefinitionMatches(installed, frozen) {
			return restoredRuntime{}, fmt.Errorf("frozen tool %q no longer matches its captured definition", frozen.Name)
		}
		restoredBindings = append(restoredBindings, installed)
	}
	securityPolicy := current.SecurityPolicy()
	if snapshot.SchemaVersion >= domain.ToolSecurityRuntimeSnapshotVersion {
		securityPolicy = snapshot.ToolSecurityPolicy
	}
	catalog, err := tools.NewCatalogWithPolicy(securityPolicy, restoredBindings...)
	if err != nil {
		return restoredRuntime{}, err
	}
	candidates := make([]domain.Agent, 0, len(snapshot.CandidateAgents))
	for _, candidate := range snapshot.CandidateAgents {
		candidates = append(candidates, restoreAgent(candidate))
	}
	client, err := r.clientFromSnapshot(snapshot)
	if err != nil {
		return restoredRuntime{}, err
	}
	return restoredRuntime{
		mode: snapshot.Mode, agent: restoreAgent(snapshot.Agent), candidateAgents: candidates,
		catalog: catalog, client: client, routerMode: NormalizeRouterMode(snapshot.RouterMode),
	}, nil
}

func validateRuntimeSnapshot(snapshot *domain.RuntimeSnapshot) error {
	if snapshot == nil {
		return ErrRuntimeSnapshotUnavailable
	}
	if snapshot.SchemaVersion != domain.PreviousRuntimeSnapshotVersion && snapshot.SchemaVersion != domain.CurrentRuntimeSnapshotVersion {
		return errors.Join(
			ErrRuntimeSnapshotResumeUnsupported,
			fmt.Errorf("snapshot schema version %d is replay-only; resumable versions are %d and %d", snapshot.SchemaVersion, domain.PreviousRuntimeSnapshotVersion, domain.CurrentRuntimeSnapshotVersion),
		)
	}
	switch snapshot.Mode {
	case ChatModeSingle:
	case ChatModeMultiAgent:
		if len(snapshot.CandidateAgents) == 0 {
			return errors.New("multi-agent runtime snapshot has no candidate agents")
		}
		if snapshot.SchemaVersion >= domain.DelegationRuntimeSnapshotVersion && (snapshot.ChildRunPolicy == nil || snapshot.ChildRunPolicy.MaxDepth != 1 || snapshot.ChildRunPolicy.TimeoutMS <= 0 || snapshot.ChildRunPolicy.SummaryMaxChars <= 0 || strings.TrimSpace(snapshot.ChildRunPolicy.AgentDefinitionSource) == "") {
			return errors.New("multi-agent runtime snapshot has no valid child run policy")
		}
	case ChatModeAutonomous:
		if snapshot.AutonomousLimits == nil {
			return errors.New("autonomous runtime snapshot has no limits")
		}
	default:
		return fmt.Errorf("runtime snapshot has invalid mode %q", snapshot.Mode)
	}
	if strings.TrimSpace(snapshot.Agent.ID) == "" {
		return errors.New("runtime snapshot has no agent")
	}
	if err := validateRuntimeExecutor(snapshot.Agent); err != nil {
		return err
	}
	for _, candidate := range snapshot.CandidateAgents {
		if err := validateRuntimeExecutor(candidate); err != nil {
			return err
		}
	}
	if strings.TrimSpace(snapshot.Model.Model) == "" {
		return errors.New("runtime snapshot has no model")
	}
	if snapshot.RunBudget == nil {
		return errors.New("runtime snapshot has no run budget")
	}
	if snapshot.SchemaVersion >= domain.ToolContractRuntimeSnapshotVersion {
		for _, tool := range snapshot.Tools {
			if tool.SchemaVersion != tools.ToolSchemaVersion || strings.TrimSpace(tool.DefinitionRevision) == "" {
				return fmt.Errorf("runtime snapshot tool %q has no valid schema contract", tool.Name)
			}
		}
	}
	if snapshot.SchemaVersion >= domain.ToolSecurityRuntimeSnapshotVersion {
		if err := toolpolicy.ValidatePolicy(snapshot.ToolSecurityPolicy); err != nil {
			return fmt.Errorf("runtime snapshot has invalid Tool security policy: %w", err)
		}
		for _, tool := range snapshot.Tools {
			if err := toolpolicy.ValidateCapability(tool.Security); err != nil {
				return fmt.Errorf("runtime snapshot tool %q has invalid security capability: %w", tool.Name, err)
			}
		}
	}
	if snapshot.SchemaVersion >= domain.ToolProgressRuntimeSnapshotVersion && !toolprogress.ValidateConfig(snapshot.ToolProgressGuard) {
		return errors.New("runtime snapshot has invalid Tool Progress Guard config")
	}
	if snapshot.Delegation != nil {
		if snapshot.SchemaVersion < domain.DelegationRuntimeSnapshotVersion || snapshot.Mode != ChatModeSingle {
			return errors.New("delegated runtime snapshot has an invalid schema or mode")
		}
		if strings.TrimSpace(snapshot.Delegation.DelegationID) == "" || strings.TrimSpace(snapshot.Delegation.ParentRunID) == "" || strings.TrimSpace(snapshot.Delegation.ParentTurnID) == "" || snapshot.Delegation.Depth != 1 || !snapshot.Delegation.IsolatedContext {
			return errors.New("delegated runtime snapshot has an invalid isolation boundary")
		}
	}
	return nil
}

func validateRuntimeExecutor(agent domain.RuntimeAgentSnapshot) error {
	executor := strings.TrimSpace(agent.Executor)
	if executor == "" || executor == domain.DefaultAgentExecutor {
		return nil
	}
	return errors.Join(ErrRuntimeExecutorUnsupported, fmt.Errorf("agent %q captured executor %q", agent.ID, executor))
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

func toolDefinitionMatches(installed tools.Binding, frozen domain.RuntimeToolSnapshot) bool {
	current := domain.RuntimeToolSnapshot{
		Name: installed.Descriptor.Name, Description: installed.Descriptor.Description,
		Parameters: installed.Descriptor.Parameters, SideEffect: string(installed.Descriptor.SideEffect.Mode),
	}
	if frozen.Security.Source != "" {
		current.Security = installed.Descriptor.Security
	}
	if frozen.DefinitionRevision != "" || frozen.SchemaVersion != "" {
		current.SchemaVersion = installed.Descriptor.SchemaVersion
		if frozen.Security.Source == "" {
			// Version 10 snapshots predate Tool security in the definition digest.
			// Structural fields still have to match before a fail-closed live policy is applied.
			legacyRevision, err := tools.LegacyDefinitionRevision(installed.Descriptor)
			if err != nil || (frozen.DefinitionRevision != legacyRevision && frozen.DefinitionRevision != installed.Descriptor.DefinitionRevision) {
				return false
			}
			current.DefinitionRevision = frozen.DefinitionRevision
		} else {
			current.DefinitionRevision = installed.Descriptor.DefinitionRevision
		}
	}
	currentJSON, err := json.Marshal(current)
	if err != nil {
		return false
	}
	frozenJSON, err := json.Marshal(frozen)
	return err == nil && bytes.Equal(currentJSON, frozenJSON)
}

func (r *Runtime) snapshotForRun(runID string) (*domain.RuntimeSnapshot, error) {
	run, ok, err := r.store.GetRun(runID)
	if err != nil {
		return nil, fmt.Errorf("load runtime snapshot for run %s: %w", runID, err)
	}
	if !ok {
		return nil, fmt.Errorf("%w for run %s", ErrRuntimeSnapshotUnavailable, runID)
	}
	if err := validateRuntimeSnapshot(run.RuntimeSnapshot); err != nil {
		return nil, fmt.Errorf("runtime snapshot for run %s is invalid: %w", runID, err)
	}
	run.RuntimeSnapshot.ContextAssembly = contextassembly.NormalizeConfig(run.RuntimeSnapshot.ContextAssembly)
	return run.RuntimeSnapshot, nil
}

func (r *Runtime) clientForRun(runID string) (modelprovider.Client, error) {
	snapshot, err := r.snapshotForRun(runID)
	if err != nil {
		return nil, err
	}
	return r.clientFromSnapshot(snapshot)
}

func (r *Runtime) clientFromSnapshot(snapshot *domain.RuntimeSnapshot) (modelprovider.Client, error) {
	current := r.modelClient.RuntimeIdentity()
	model := snapshot.Model
	if r.modelClient.HasAPIKey() && current.Provider != model.Provider {
		return nil, fmt.Errorf("credential for frozen provider %q is unavailable; current provider is %q", model.Provider, current.Provider)
	}
	return r.modelClient.WithRuntimeIdentity(modelprovider.RuntimeIdentity{
		Provider: model.Provider, BaseURL: model.BaseURL, Model: model.Model,
		EmbeddingBaseURL: model.EmbeddingBaseURL, EmbeddingModel: model.EmbeddingModel,
		EmbeddingDimensions: model.EmbeddingDimensions,
	}), nil
}

func (r *Runtime) limitsForRun(runID string) (AutonomousLimits, error) {
	snapshot, err := r.snapshotForRun(runID)
	if err != nil {
		return AutonomousLimits{}, err
	}
	limits, err := autonomousLimitsFromSnapshot(snapshot)
	if err != nil {
		return AutonomousLimits{}, fmt.Errorf("%w for autonomous run %s: %v", ErrRuntimeSnapshotUnavailable, runID, err)
	}
	return limits, nil
}

func autonomousLimitsFromSnapshot(snapshot *domain.RuntimeSnapshot) (AutonomousLimits, error) {
	if snapshot == nil {
		return AutonomousLimits{}, errors.New("snapshot is missing")
	}
	if snapshot.AutonomousLimits == nil {
		return AutonomousLimits{}, errors.New("limits are missing")
	}
	if snapshot.RunBudget == nil {
		return AutonomousLimits{}, errors.New("run budget is missing")
	}
	frozen := snapshot.AutonomousLimits
	limits := normalizeAutonomousLimits(AutonomousLimits{
		MaxIterations:  frozen.MaxIterations,
		MaxOutputChars: frozen.MaxOutputChars,
	})
	limits.MaxRuntime = time.Duration(snapshot.RunBudget.MaxRuntimeMS) * time.Millisecond
	limits.MaxToolCalls = snapshot.RunBudget.MaxToolCalls
	return limits, nil
}
