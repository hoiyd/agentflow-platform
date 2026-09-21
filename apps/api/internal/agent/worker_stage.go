package agent

import (
	"context"
	"fmt"
	"strings"

	turnpkg "agentflow-platform/apps/api/internal/agent/turn"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/taskstate"
	"agentflow-platform/apps/api/internal/tool"
)

const workerHandoffMaxCharacters = 4000

// runWorkerStage preserves the useful delegation boundary without creating a
// second Run: the selected agent receives only the explicit task, its frozen
// tool allowlist, and explicitly retrieved memory/knowledge.
func (r *Runtime) runWorkerStage(ctx context.Context, events chan<- domain.RunEvent, prepared PreparedCollaborationRun, catalog *tool.Catalog, input string, query retrievalQuery) (string, error) {
	step, err := r.store.CreateCollaborationStep(domain.CollaborationStep{
		RunID: prepared.Run.ID, ConversationID: prepared.Run.ConversationID, Role: "worker",
		AgentID: prepared.WorkerAgent.ID, Status: domain.CollaborationStepRunning, Input: input,
	})
	if err != nil {
		return "", err
	}
	emitCollaborationEvent(events, liveStageEvent(step))
	if err := r.publishStage(ctx, step, domain.EventStageStarted); err != nil {
		return "", err
	}

	workerCatalog, err := catalogForAgent(catalog, prepared.WorkerAgent)
	if err != nil {
		return "", r.failWorkerStage(ctx, events, step, err)
	}
	query.StageID = step.ID
	retrievedMemories, retrievedChunks := r.retrieveContext(ctx, prepared.Run.ID, query, prepared.WorkerAgent.MemoryEnabled, prepared.WorkerAgent.RetrievalEnabled, map[string]any{
		"executor": domain.DefaultAgentExecutor, "framework": "agentflow-native", "isolated_stage": true,
	})
	result, err := r.turnEngine.Execute(ctx, turnpkg.Request{
		RunID: prepared.Run.ID, StepID: step.ID, ConversationID: prepared.Run.ConversationID,
		Agent: prepared.WorkerAgent, Role: "worker", SystemPrompt: workerPrompt(prepared.WorkerAgent), Input: input,
		Catalog: workerCatalog,
		Context: turnpkg.Context{Memories: retrievedMemories, Chunks: retrievedChunks, Isolated: true}, Sink: r.runEventSink(),
	}, nil)
	if err != nil {
		return "", r.failWorkerStage(ctx, events, step, err)
	}
	completed, err := r.store.UpdateCollaborationStep(step.ID, domain.CollaborationStepCompleted, result.Output, "")
	if err != nil {
		return "", err
	}
	emitCollaborationEvent(events, liveStageEvent(completed))
	if err := r.publishStage(ctx, completed, domain.EventStageCompleted); err != nil {
		return "", err
	}
	return boundedWorkerHandoff(completed), nil
}

func catalogForAgent(catalog *tool.Catalog, agent domain.Agent) (*tool.Catalog, error) {
	if catalog == nil {
		return tool.NewCatalog()
	}
	bindings := make([]tool.Binding, 0, len(agent.Tools))
	for _, name := range agent.Tools {
		if name == taskstate.UpdateToolName {
			continue
		}
		if binding, ok := catalog.Resolve(name); ok {
			bindings = append(bindings, binding)
		}
	}
	return tool.NewCatalogWithPolicy(catalog.SecurityPolicy(), bindings...)
}

func boundedWorkerHandoff(step domain.CollaborationStep) string {
	output := strings.TrimSpace(step.Output)
	runes := []rune(output)
	if len(runes) <= workerHandoffMaxCharacters {
		return output
	}
	suffix := []rune(fmt.Sprintf("\n...[worker output truncated; see stage]\n\nFull output: run://%s/stages/%s", step.RunID, step.ID))
	limit := workerHandoffMaxCharacters - len(suffix)
	if limit < 0 {
		limit = 0
	}
	return string(append(runes[:limit], suffix...))
}

func (r *Runtime) failWorkerStage(ctx context.Context, events chan<- domain.RunEvent, step domain.CollaborationStep, cause error) error {
	failed, err := r.store.UpdateCollaborationStep(step.ID, domain.CollaborationStepFailed, "", cause.Error())
	if err == nil {
		emitCollaborationEvent(events, liveStageEvent(failed))
		_ = r.publishStage(ctx, failed, domain.EventStageFailed)
	}
	return cause
}

func emitCollaborationEvent(events chan<- domain.RunEvent, event domain.RunEvent) {
	if events != nil {
		events <- event
	}
}
