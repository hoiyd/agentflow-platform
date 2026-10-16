package agent

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/skill"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
	"agentflow-platform/apps/api/internal/tool/progress"
	"errors"
	"fmt"
	"strings"
)

func validateRuntimeSnapshot(snapshot *domain.RuntimeSnapshot) error {
	if snapshot == nil {
		return ErrRuntimeSnapshotUnavailable
	}
	if snapshot.ExecutionConstraint != "" && snapshot.ExecutionConstraint != domain.ExecutionPlanOnly {
		return errors.New("invalid frozen execution constraint")
	}
	if snapshot.ExecutionConstraint == domain.ExecutionPlanOnly {
		if snapshot.PlanOrigin != nil {
			return errors.New("planning Snapshot cannot carry execution approval")
		}
		for _, item := range snapshot.Tools {
			if !policy.AllowsPlanning(item.Security) {
				return errors.New("planning Snapshot contains a mutable Tool")
			}
		}
	}
	if snapshot.SchemaVersion != domain.CurrentRuntimeSnapshotVersion {
		return errors.Join(
			ErrRuntimeSnapshotResumeUnsupported,
			fmt.Errorf("snapshot schema version %d is replay-only; resumable version is %d", snapshot.SchemaVersion, domain.CurrentRuntimeSnapshotVersion),
		)
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
	if _, err := skill.Bound(snapshot, snapshot.Agent.ID); err != nil {
		return err
	}
	if err := validateRuntimeExecutor(snapshot.Agent); err != nil {
		return err
	}
	for _, candidate := range snapshot.CandidateAgents {
		if _, err := skill.Bound(snapshot, candidate.ID); err != nil {
			return err
		}
		if err := validateRuntimeExecutor(candidate); err != nil {
			return err
		}
	}
	if strings.TrimSpace(snapshot.Embedding.Provider) == "" || strings.TrimSpace(snapshot.Embedding.Model) == "" || strings.TrimSpace(snapshot.Embedding.BaseURL) == "" || snapshot.Embedding.Dimensions <= 0 {
		return errors.New("runtime snapshot has no valid embedding service")
	}
	if err := validateModelRoutingSnapshot(snapshot.ModelRouting); err != nil {
		return err
	}
	for _, route := range snapshot.ModelRouting.Routes {
		if route.GenerationPolicy == nil {
			return fmt.Errorf("runtime snapshot route %q has no frozen generation policy", route.ID)
		}
		if err := route.GenerationPolicy.Validate(route.Capabilities.Seed); err != nil {
			return fmt.Errorf("runtime snapshot route %q has invalid generation policy: %w", route.ID, err)
		}
	}
	if snapshot.RunBudget == nil {
		return errors.New("runtime snapshot has no run budget")
	}
	for _, snapshotTool := range snapshot.Tools {
		if snapshotTool.SchemaVersion != tool.ToolSchemaVersion || strings.TrimSpace(snapshotTool.DefinitionRevision) == "" {
			return fmt.Errorf("runtime snapshot tool %q has no valid schema contract", snapshotTool.Name)
		}
	}
	if err := policy.ValidatePolicy(snapshot.ToolSecurityPolicy); err != nil {
		return fmt.Errorf("runtime snapshot has invalid Tool security policy: %w", err)
	}
	for _, tool := range snapshot.Tools {
		if err := policy.ValidateCapability(tool.Security); err != nil {
			return fmt.Errorf("runtime snapshot tool %q has invalid security capability: %w", tool.Name, err)
		}
	}
	if !progress.ValidateConfig(snapshot.ToolProgressGuard) {
		return errors.New("runtime snapshot has invalid Tool Progress Guard config")
	}
	if snapshot.ToolSchema != nil {
		if err := snapshot.ToolSchema.Validate(); err != nil {
			return err
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
