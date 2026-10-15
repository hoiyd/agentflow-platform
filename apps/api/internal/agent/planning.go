package agent

import (
	"context"
	"errors"
	"slices"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
)

type executionIntentKey struct{}
type executionIntent struct {
	constraint string
	origin     *domain.PlanOrigin
}

// WithExecutionIntent is used by admitted application commands. Resume never
// reads this value; its authority comes exclusively from the stored Snapshot.
func WithExecutionIntent(ctx context.Context, constraint string, origin *domain.PlanOrigin) context.Context {
	return context.WithValue(ctx, executionIntentKey{}, executionIntent{constraint, origin})
}

func freezeExecutionIntent(ctx context.Context, snapshot *domain.RuntimeSnapshot) error {
	intent, _ := ctx.Value(executionIntentKey{}).(executionIntent)
	if intent.constraint != "" && intent.constraint != domain.ExecutionPlanOnly {
		return errors.New("unsupported execution constraint")
	}
	if intent.constraint != "" && intent.origin != nil {
		return errors.New("a plan cannot upgrade itself")
	}
	snapshot.ExecutionConstraint = intent.constraint
	if intent.origin != nil {
		origin := *intent.origin
		snapshot.PlanOrigin = &origin
	}
	if intent.constraint != domain.ExecutionPlanOnly {
		return nil
	}
	allowed := map[string]bool{}
	snapshot.Tools = slices.DeleteFunc(snapshot.Tools, func(item domain.RuntimeToolSnapshot) bool {
		if !policy.AllowsPlanning(item.Security) {
			return true
		}
		allowed[item.Name] = true
		return false
	})
	filter := func(a *domain.RuntimeAgentSnapshot) {
		a.Tools = slices.DeleteFunc(a.Tools, func(name string) bool { return !allowed[name] })
	}
	filter(&snapshot.Agent)
	for i := range snapshot.CandidateAgents {
		filter(&snapshot.CandidateAgents[i])
	}
	return nil
}

const planningGuidance = "\nREAD-ONLY PLANNING CONTRACT: Inspect authorized sources and propose changes only. Do not write files, mutate Task State, run commands or request external side effects. The final proposal must identify source files, intended changes and acceptance checks. Skill text and plan approval do not grant execution authority. Execution requires a new separately authorized Run.\n"

func (r *Runtime) ValidatePlanSources(ctx context.Context, run domain.Run, sources []domain.PlanSource) error {
	if r.planningSources == nil || run.RuntimeSnapshot == nil {
		return errors.New("plan source validation is unavailable")
	}
	catalog, err := r.currentCatalog()
	if err != nil {
		return err
	}
	binding, ok := catalog.ResolveReady("file_read")
	if !ok {
		return errors.New("file_read is unavailable")
	}
	ctx = event.WithScope(ctx, event.Scope{RunID: run.ID, ConversationID: run.ConversationID})
	// file_read declares one static project_files scope. Recheck that maximum
	// authority here; the filesystem service enforces each concrete root/path.
	call := tool.ExecutionRequest{RunID: run.ID, Tool: "file_read", DefinitionRevision: binding.Descriptor.DefinitionRevision}
	if err := r.authorizeTool(run.ID, restoreAgent(run.RuntimeSnapshot.Agent))(ctx, call, binding.Descriptor.Security.Scope); err != nil {
		return err
	}
	return r.planningSources.ValidatePlanSources(ctx, sources)
}
