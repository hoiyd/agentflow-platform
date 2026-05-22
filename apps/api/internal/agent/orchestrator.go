package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

const (
	ChatModeSingle     = "single"
	ChatModeMultiAgent = "multi_agent"
)

type PreparedCollaborationRun struct {
	WorkerAgent domain.Agent
	Run         domain.Run
}

type CollaborationEvent struct {
	Type  string
	Step  domain.CollaborationStep
	Delta string
}

func (r *Runtime) PrepareCollaborationRun(ctx context.Context, agentID string, conversationID string) (PreparedCollaborationRun, error) {
	agent, err := r.resolveAgent(strings.TrimSpace(agentID))
	if err != nil {
		return PreparedCollaborationRun{}, err
	}

	run, err := r.store.CreateRun(agent.ID, conversationID)
	if err != nil {
		return PreparedCollaborationRun{}, err
	}
	run, err = r.store.UpdateRunStatus(run.ID, domain.RunRunning, "")
	if err != nil {
		return PreparedCollaborationRun{}, err
	}
	return PreparedCollaborationRun{WorkerAgent: agent, Run: run}, nil
}

func (r *Runtime) RunCollaboration(ctx context.Context, prepared PreparedCollaborationRun, task string) (<-chan CollaborationEvent, <-chan error) {
	events := make(chan CollaborationEvent)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		_, err := r.runCollaborationStep(ctx, events, prepared, "planner", "", plannerPrompt(), task)
		if err != nil {
			errs <- err
			return
		}
		_, err = r.store.UpdateRunStatus(prepared.Run.ID, domain.RunWaitingForUser, "")
		if err != nil {
			errs <- err
			return
		}
	}()

	return events, errs
}

func (r *Runtime) ContinueCollaboration(ctx context.Context, runID string, plan string) (<-chan CollaborationEvent, <-chan error) {
	events := make(chan CollaborationEvent)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		run, ok, err := r.store.GetRun(strings.TrimSpace(runID))
		if err != nil {
			errs <- err
			return
		}
		if !ok {
			errs <- store.ErrNotFound("run")
			return
		}
		if run.Status != domain.RunWaitingForUser {
			errs <- fmt.Errorf("run is not waiting for user input")
			return
		}

		agent, ok, err := r.store.GetAgent(run.AgentID)
		if err != nil {
			errs <- err
			return
		}
		if !ok {
			errs <- store.ErrNotFound("agent")
			return
		}

		steps, err := r.store.ListCollaborationSteps(run.ID)
		if err != nil {
			errs <- err
			return
		}
		plannerStep, ok := findCollaborationStep(steps, "planner")
		if !ok {
			errs <- fmt.Errorf("planner step not found")
			return
		}
		plan = strings.TrimSpace(plan)
		if plan == "" {
			plan = plannerStep.Output
		}
		task := plannerStep.Input
		updatedPlan, err := r.store.UpdateCollaborationStepOutput(plannerStep.ID, plan)
		if err != nil {
			errs <- err
			return
		}
		events <- CollaborationEvent{Type: "collaboration_step", Step: updatedPlan}

		run, err = r.store.UpdateRunStatus(run.ID, domain.RunRunning, "")
		if err != nil {
			errs <- err
			return
		}
		prepared := PreparedCollaborationRun{WorkerAgent: agent, Run: run}

		workerInput := fmt.Sprintf("User task:\n%s\n\nPlanner output:\n%s", task, plan)
		worker, err := r.runCollaborationStep(ctx, events, prepared, "worker", prepared.WorkerAgent.ID, workerPrompt(prepared.WorkerAgent), workerInput)
		if err != nil {
			errs <- err
			return
		}

		reviewInput := fmt.Sprintf("User task:\n%s\n\nPlan:\n%s\n\nWorker result:\n%s", task, plan, worker)
		review, err := r.runCollaborationStep(ctx, events, prepared, "reviewer", "", reviewerPrompt(), reviewInput)
		if err != nil {
			errs <- err
			return
		}

		finalInput := fmt.Sprintf("User task:\n%s\n\nPlan:\n%s\n\nWorker result:\n%s\n\nReview:\n%s", task, plan, worker, review)
		final, err := r.runCollaborationStep(ctx, events, prepared, "finalizer", "", finalizerPrompt(), finalInput)
		if err != nil {
			errs <- err
			return
		}
		emitFinalDeltas(ctx, final, events)
	}()

	return events, errs
}

func findCollaborationStep(steps []domain.CollaborationStep, role string) (domain.CollaborationStep, bool) {
	for _, step := range steps {
		if step.Role == role {
			return step, true
		}
	}
	return domain.CollaborationStep{}, false
}

func (r *Runtime) runCollaborationStep(ctx context.Context, events chan<- CollaborationEvent, prepared PreparedCollaborationRun, role string, agentID string, systemPrompt string, input string) (string, error) {
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
	events <- CollaborationEvent{Type: "collaboration_step", Step: step}

	output, err := r.openAI.CompleteText(ctx, systemPrompt, input)
	if err != nil {
		failed, updateErr := r.store.UpdateCollaborationStep(step.ID, domain.CollaborationStepFailed, "", err.Error())
		if updateErr == nil {
			events <- CollaborationEvent{Type: "collaboration_step", Step: failed}
		}
		return "", err
	}
	completed, err := r.store.UpdateCollaborationStep(step.ID, domain.CollaborationStepCompleted, output, "")
	if err != nil {
		return "", err
	}
	events <- CollaborationEvent{Type: "collaboration_step", Step: completed}
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

func emitFinalDeltas(ctx context.Context, text string, events chan<- CollaborationEvent) {
	parts := strings.SplitAfter(strings.TrimSpace(text), " ")
	for _, part := range parts {
		if part == "" {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case events <- CollaborationEvent{Type: "delta", Delta: part}:
			time.Sleep(15 * time.Millisecond)
		}
	}
}

func NormalizeChatMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case "", ChatModeSingle:
		return ChatModeSingle
	case ChatModeMultiAgent:
		return ChatModeMultiAgent
	default:
		return ChatModeSingle
	}
}

func IsAgentNotFound(err error) bool {
	return store.IsNotFound(err)
}
