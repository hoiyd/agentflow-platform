package agent

import (
	"agentflow-platform/apps/api/internal/contextassembly"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/inference/provider"
	"errors"
	"fmt"
	"time"
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

func (r *Runtime) embeddingClientForRun(runID string) (provider.Client, error) {
	snapshot, err := r.snapshotForRun(runID)
	if err != nil {
		return nil, err
	}
	return r.embeddingClientFromSnapshot(snapshot)
}

func (r *Runtime) embeddingClientFromSnapshot(snapshot *domain.RuntimeSnapshot) (provider.Client, error) {
	if r.embeddingClient == nil {
		return nil, errors.New("embedding client is unavailable")
	}
	current := r.embeddingClient.RuntimeIdentity()
	embedding := snapshot.Embedding
	if current.EmbeddingProvider != embedding.Provider {
		return nil, fmt.Errorf("credential for frozen embedding provider %q is unavailable; current provider is %q", embedding.Provider, current.EmbeddingProvider)
	}
	return r.embeddingClient.WithRuntimeIdentity(provider.RuntimeIdentity{
		Provider: current.Provider, BaseURL: current.BaseURL, Model: current.Model,
		EmbeddingProvider: embedding.Provider, EmbeddingBaseURL: embedding.BaseURL,
		EmbeddingModel: embedding.Model, EmbeddingDimensions: embedding.Dimensions,
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
