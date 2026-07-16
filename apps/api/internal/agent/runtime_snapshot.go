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
	"agentflow-platform/apps/api/internal/openai"
	"agentflow-platform/apps/api/internal/tools"
)

var ErrRuntimeSnapshotUnavailable = errors.New("runtime snapshot is unavailable")

type restoredRuntime struct {
	mode            string
	agent           domain.Agent
	candidateAgents []domain.Agent
	catalog         *tools.Catalog
	client          *openai.Client
	routerMode      string
}

func (r *Runtime) captureRuntimeSnapshot(mode string, agent domain.Agent, candidates []domain.Agent, executorOverride string) (domain.RuntimeSnapshot, error) {
	catalog, err := r.currentCatalog()
	if err != nil {
		return domain.RuntimeSnapshot{}, err
	}
	agent = domain.NormalizeAgentConfig(agent)
	if executor := strings.TrimSpace(executorOverride); executor != "" {
		agent.Executor = NormalizeExecutorKind(executor)
	}
	candidateSnapshots := make([]domain.RuntimeAgentSnapshot, 0, len(candidates))
	toolNames := append([]string(nil), agent.Tools...)
	for _, candidate := range candidates {
		candidate = domain.NormalizeAgentConfig(candidate)
		candidateSnapshots = append(candidateSnapshots, snapshotAgent(candidate))
		toolNames = append(toolNames, candidate.Tools...)
	}
	toolSnapshots := snapshotTools(catalog, toolNames)
	identity := r.openAI.RuntimeIdentity()
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
		Tools:           toolSnapshots,
		ContextAssembly: contextassembly.NormalizeConfig(r.contextAssemblyConfig),
		RouterMode:      r.routerMode,
		CreatedAt:       time.Now().UTC(),
	}
	if mode == ChatModeAutonomous {
		snapshot.AutonomousLimits = &domain.RuntimeLimitsSnapshot{
			MaxIterations:  r.autonomousLimits.MaxIterations,
			MaxRuntimeMS:   r.autonomousLimits.MaxRuntime.Milliseconds(),
			MaxOutputChars: r.autonomousLimits.MaxOutputChars,
			MaxToolCalls:   r.autonomousLimits.MaxToolCalls,
		}
	}
	return snapshot, nil
}

func (r *Runtime) currentCatalog() (*tools.Catalog, error) {
	if r.tools == nil {
		return tools.DefaultCatalog(), nil
	}
	return r.tools.Catalog()
}

func snapshotAgent(agent domain.Agent) domain.RuntimeAgentSnapshot {
	return domain.RuntimeAgentSnapshot{
		ID: agent.ID, Name: agent.Name, Description: agent.Description, SystemPrompt: agent.SystemPrompt,
		Tools: append([]string(nil), agent.Tools...), MemoryEnabled: agent.MemoryEnabled,
		RetrievalEnabled: agent.RetrievalEnabled, Executor: agent.Executor,
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
		Executor:         snapshot.Executor,
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
			Parameters: binding.Descriptor.Parameters,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

func (r *Runtime) restoreRuntime(run domain.Run) (restoredRuntime, error) {
	if err := validateRuntimeSnapshot(run.RuntimeSnapshot); err != nil {
		return restoredRuntime{}, fmt.Errorf("%w for run %s: %v; run cannot be resumed safely", ErrRuntimeSnapshotUnavailable, run.ID, err)
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
	catalog, err := tools.NewCatalog(restoredBindings...)
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
	if snapshot == nil || (snapshot.SchemaVersion != domain.LegacyRuntimeSnapshotVersion && snapshot.SchemaVersion != domain.CurrentRuntimeSnapshotVersion) {
		return ErrRuntimeSnapshotUnavailable
	}
	switch snapshot.Mode {
	case ChatModeSingle:
	case ChatModeMultiAgent:
		if len(snapshot.CandidateAgents) == 0 {
			return errors.New("multi-agent runtime snapshot has no candidate agents")
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
	if strings.TrimSpace(snapshot.Model.Model) == "" {
		return errors.New("runtime snapshot has no model")
	}
	return nil
}

func toolDefinitionMatches(installed tools.Binding, frozen domain.RuntimeToolSnapshot) bool {
	currentJSON, err := json.Marshal(domain.RuntimeToolSnapshot{
		Name: installed.Descriptor.Name, Description: installed.Descriptor.Description,
		Parameters: installed.Descriptor.Parameters,
	})
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
		return nil, fmt.Errorf("%w for run %s: %v", ErrRuntimeSnapshotUnavailable, runID, err)
	}
	run.RuntimeSnapshot.ContextAssembly = contextassembly.NormalizeConfig(run.RuntimeSnapshot.ContextAssembly)
	return run.RuntimeSnapshot, nil
}

func (r *Runtime) clientForRun(runID string) (*openai.Client, error) {
	snapshot, err := r.snapshotForRun(runID)
	if err != nil {
		return nil, err
	}
	return r.clientFromSnapshot(snapshot)
}

func (r *Runtime) clientFromSnapshot(snapshot *domain.RuntimeSnapshot) (*openai.Client, error) {
	current := r.openAI.RuntimeIdentity()
	model := snapshot.Model
	if r.openAI.HasAPIKey() && current.Provider != model.Provider {
		return nil, fmt.Errorf("credential for frozen provider %q is unavailable; current provider is %q", model.Provider, current.Provider)
	}
	return r.openAI.WithRuntimeIdentity(openai.RuntimeIdentity{
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
	if snapshot.AutonomousLimits == nil {
		return AutonomousLimits{}, fmt.Errorf("%w for autonomous run %s: limits are missing", ErrRuntimeSnapshotUnavailable, runID)
	}
	frozen := snapshot.AutonomousLimits
	return normalizeAutonomousLimits(AutonomousLimits{
		MaxIterations: frozen.MaxIterations, MaxRuntime: time.Duration(frozen.MaxRuntimeMS) * time.Millisecond,
		MaxOutputChars: frozen.MaxOutputChars, MaxToolCalls: frozen.MaxToolCalls,
	}), nil
}
