package app

import (
	"context"

	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/inference/requestcontrol"
	"agentflow-platform/apps/api/internal/store"
)

// Background work has no browser Session. Its persisted Run points to the
// immutable Workspace owner; neither request headers nor Tool inputs participate.
func modelOwnerResolver(s *store.PostgresStore) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		owner := requestcontrol.OwnerFromContext(ctx)
		if runID := event.ScopeFromContext(ctx).RunID; runID != "" {
			run, ok, err := s.GetRun(runID)
			if err != nil {
				if ctx.Err() != nil {
					return "", ctx.Err()
				}
				return "", &requestcontrol.OwnerAdmissionError{Code: "model_owner_resolution_failed"}
			}
			if !ok {
				return "", &requestcontrol.OwnerAdmissionError{Code: "model_owner_unavailable"}
			}
			persisted, err := s.WorkspaceModelOwner(ctx, run.WorkspaceID)
			if store.IsNotFound(err) || err == nil && owner != "" && owner != persisted {
				return "", &requestcontrol.OwnerAdmissionError{Code: "model_owner_unavailable"}
			}
			if err != nil {
				if ctx.Err() != nil {
					return "", ctx.Err()
				}
				return "", &requestcontrol.OwnerAdmissionError{Code: "model_owner_resolution_failed"}
			}
			return persisted, nil
		}
		if owner == "" {
			return "", &requestcontrol.OwnerAdmissionError{Code: "model_owner_required"}
		}
		return owner, nil
	}
}
