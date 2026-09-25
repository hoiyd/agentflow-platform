package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	turnpkg "agentflow-platform/apps/api/internal/agent/turn"
	"agentflow-platform/apps/api/internal/domain"
)

func (r *Runtime) finishCollaboration(ctx context.Context, events chan<- domain.RunEvent, prepared PreparedCollaborationRun, task, plan, worker string) error {
	reviewInput := fmt.Sprintf("User task:\n%s\n\nPlan:\n%s\n\nWorker result:\n%s", task, plan, worker)
	review, err := r.runCollaborationStep(ctx, events, prepared, "reviewer", "", reviewerPrompt(), reviewInput, userInputRetrievalQuery(task))
	if err != nil {
		return err
	}
	finalInput := fmt.Sprintf("User task:\n%s\n\nPlan:\n%s\n\nWorker result:\n%s\n\nReview:\n%s", task, plan, worker, review)
	final, err := r.runCollaborationStep(ctx, events, prepared, "finalizer", "", finalizerPrompt(), finalInput, userInputRetrievalQuery(task))
	if err != nil {
		return err
	}
	r.emitFinalDeltas(ctx, prepared.Run.ID, final, events)
	return nil
}

func findCollaborationStep(steps []domain.CollaborationStep, role string) (domain.CollaborationStep, bool) {
	for _, step := range steps {
		if step.Role == role {
			return step, true
		}
	}
	return domain.CollaborationStep{}, false
}

func latestCompletedCollaborationStep(steps []domain.CollaborationStep, role string) (domain.CollaborationStep, bool) {
	for index := len(steps) - 1; index >= 0; index-- {
		if steps[index].Role == role && steps[index].Status == domain.CollaborationStepCompleted {
			return steps[index], true
		}
	}
	return domain.CollaborationStep{}, false
}

func findAgentByID(agents []domain.Agent, id string) (domain.Agent, bool) {
	for _, item := range agents {
		if item.ID == id {
			return item, true
		}
	}
	return domain.Agent{}, false
}

func (r *Runtime) runCollaborationStep(ctx context.Context, events chan<- domain.RunEvent, prepared PreparedCollaborationRun, role string, agentID string, systemPrompt string, input string, query retrievalQuery) (string, error) {
	step, err := r.store.CreateCollaborationStep(domain.CollaborationStep{
		RunID:          prepared.Run.ID,
		ConversationID: prepared.Run.ConversationID,
		Role:           role,
		AgentID:        agentID,
		Status:         domain.CollaborationStepRunning,
		Input:          input,
	})
	if err != nil {
		return "", err
	}
	events <- liveStageEvent(step)
	if err := r.publishStage(ctx, step, domain.EventStageStarted); err != nil {
		return "", err
	}

	query.StageID = step.ID
	retrievedMemories, retrievedChunks := r.retrieveContext(ctx, prepared.Run.ID, query, true, true, map[string]any{
		"executor":   domain.DefaultAgentExecutor,
		"framework":  "agentflow-native",
		"stage_role": role,
	})
	result, err := r.turnEngine.Execute(ctx, turnpkg.Request{
		RunID: prepared.Run.ID, StepID: step.ID, ConversationID: prepared.Run.ConversationID,
		Agent: domain.Agent{ID: agentID, SystemPrompt: systemPrompt}, Role: role,
		SystemPrompt: systemPrompt, Input: input, ModelMode: turnpkg.ModelModeText,
		Context: turnpkg.Context{Memories: retrievedMemories, Chunks: retrievedChunks},
		Sink:    r.runEventSink(),
	}, nil)
	if err != nil {
		failed, updateErr := r.store.UpdateCollaborationStep(step.ID, domain.CollaborationStepFailed, "", err.Error())
		if updateErr == nil {
			events <- liveStageEvent(failed)
			if checkpointErr := r.publishStage(ctx, failed, domain.EventStageFailed); checkpointErr != nil {
				return "", fmt.Errorf("stage failed: %w; persist checkpoint: %v", err, checkpointErr)
			}
		}
		return "", err
	}
	output := result.Output
	completed, err := r.store.UpdateCollaborationStep(step.ID, domain.CollaborationStepCompleted, output, "")
	if err != nil {
		return "", err
	}
	events <- liveStageEvent(completed)
	if err := r.publishStage(ctx, completed, domain.EventStageCompleted); err != nil {
		return "", err
	}
	return output, nil
}

func plannerPrompt() string {
	return "You are the Planner collaboration role. Break the user task into 2-5 concrete steps, name key assumptions, and define success criteria. Do not execute the task."
}

func workerPrompt(agent domain.Agent) string {
	return fmt.Sprintf("You are the Worker collaboration role. Execute the plan using this selected agent persona.\n\nWorker agent: %s\nDescription: %s\nPersona instructions: %s\n\nProduce the best task result you can. Do not review your own work.",
		agent.Name,
		agent.Description,
		agent.SystemPrompt,
	)
}

func reviewerPrompt() string {
	return "You are the Reviewer collaboration role. Check the worker result against the user task and plan. Identify quality issues, risks, missing details, and whether the result is ready. Do not rewrite the final answer."
}

func finalizerPrompt() string {
	return "You are the Finalizer collaboration role. Produce the final user-facing answer by combining the plan, worker result, and reviewer notes. Be concise and do not mention internal implementation details unless relevant."
}

func (r *Runtime) emitFinalDeltas(ctx context.Context, runID string, text string, events chan<- domain.RunEvent) {
	parts := strings.SplitAfter(strings.TrimSpace(text), " ")
	for _, part := range parts {
		if part == "" {
			continue
		}
		item := liveDeltaEvent(runID, part)
		select {
		case <-ctx.Done():
			return
		case events <- item:
			r.publishLive(item)
			time.Sleep(15 * time.Millisecond)
		}
	}
}
