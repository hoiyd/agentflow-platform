package agent

import (
	"context"
	"fmt"
	"log"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/store"
)

const (
	ChatModeSingle     = "single"
	ChatModeMultiAgent = "multi_agent"
	ChatModeAutonomous = "autonomous"
	RouterModeAuto     = "auto"
	RouterModeQuery    = "query_match"

	AgentSelectionOutcomeSelected     = "selected"
	AgentSelectionOutcomeNoEligible   = "no_eligible_agent"
	AgentSelectionOutcomeNoSuitable   = "no_suitable_agent"
	AgentSelectionOutcomeRouterFailed = "router_failed"
)

var ErrNoEligibleAgent = failure.New(failure.Definition{
	Message: "no eligible worker agent is available",
	Info: failure.Info{
		Code: "agent_route_no_eligible_candidate", Source: "agent_router",
		Category: failure.CategoryValidation, Retryable: false,
	},
})

var ErrNoSuitableAgent = failure.New(failure.Definition{
	Message: "no suitable worker agent matched the task",
	Info: failure.Info{
		Code: "agent_route_no_suitable_candidate", Source: "agent_router",
		Category: failure.CategoryValidation, Retryable: false,
	},
})

var ErrInvalidRoutingRequirements = failure.New(failure.Definition{
	Message: "routing requirements are invalid",
	Info: failure.Info{
		Code: "agent_route_requirements_invalid", Source: "agent_router",
		Category: failure.CategoryValidation, Retryable: false,
	},
})

var ErrAgentRouteResponseInvalid = failure.New(failure.Definition{
	Message: "agent router returned an invalid selection",
	Info: failure.Info{
		Code: "agent_route_response_invalid", Source: "agent_router",
		Category: failure.CategoryExecution, Retryable: true,
	},
})

type PreparedCollaborationRun struct {
	WorkerAgent domain.Agent
	Run         domain.Run
}

type AutonomousProgress struct {
	Iteration         int
	MaxIterations     int
	ElapsedSeconds    int
	MaxRuntimeSeconds int
	OutputChars       int
	MaxOutputChars    int
	ToolCalls         int
	MaxToolCalls      int
	StopReason        string
}

func liveRunEvent(run domain.Run) domain.RunEvent {
	eventType := domain.EventRunStarted
	switch run.Status {
	case domain.RunWaitingForUser:
		eventType = domain.EventRunWaitingForUser
	case domain.RunCompleted:
		eventType = domain.EventRunCompleted
	case domain.RunFailed, domain.RunFailedRecoverable:
		eventType = domain.EventRunFailed
	case domain.RunCanceling:
		eventType = domain.EventRunCancelRequested
	case domain.RunCanceled:
		eventType = domain.EventRunCanceled
	}
	return domain.RunEvent{Type: eventType, SchemaVersion: domain.CurrentRunEventSchemaVersion, RunID: run.ID,
		ConversationID: run.ConversationID, Payload: map[string]any{"status": run.Status, "agent_id": run.AgentID, "error": run.Error}}
}

func liveStageEvent(step domain.CollaborationStep) domain.RunEvent {
	eventType := domain.EventStageStarted
	if step.Status == domain.CollaborationStepCompleted {
		eventType = domain.EventStageCompleted
	}
	if step.Status == domain.CollaborationStepFailed {
		eventType = domain.EventStageFailed
	}
	return domain.RunEvent{Type: eventType, SchemaVersion: domain.CurrentRunEventSchemaVersion, RunID: step.RunID,
		ConversationID: step.ConversationID, StageID: step.ID, Payload: map[string]any{
			"name": step.Role, "agent_id": step.AgentID, "status": step.Status, "iteration": step.Iteration,
			"input": step.Input, "output": step.Output, "error": step.Error,
		}}
}

func liveDeltaEvent(runID, delta string) domain.RunEvent {
	return domain.RunEvent{Type: domain.EventModelDelta, SchemaVersion: domain.CurrentRunEventSchemaVersion, RunID: runID, Payload: map[string]any{"delta": delta}}
}

func (r *Runtime) PrepareCollaborationRunWithContract(ctx context.Context, agentID string, conversationID string, contract *domain.CompletionContract) (PreparedCollaborationRun, error) {
	agent, err := r.resolveAgent(agentID)
	if err != nil {
		return PreparedCollaborationRun{}, err
	}

	agents, err := r.store.ListAgents()
	if err != nil {
		return PreparedCollaborationRun{}, err
	}
	snapshot, err := r.captureRuntimeSnapshot(ChatModeMultiAgent, agent, agents)
	if err != nil {
		return PreparedCollaborationRun{}, err
	}
	agent = restoreAgent(snapshot.Agent)
	run, err := r.store.CreateRunWithContract(agent.ID, conversationID, snapshot, contract)
	if err != nil {
		return PreparedCollaborationRun{}, err
	}
	run, err = r.store.UpdateRunStatus(run.ID, domain.RunRunning, "")
	if err != nil {
		return PreparedCollaborationRun{}, err
	}
	r.publishRunLifecycle(ctx, run, domain.EventRunCreated, map[string]any{"status": domain.RunQueued})
	r.publishRunLifecycle(ctx, run, domain.EventRunStarted, map[string]any{"status": run.Status})
	log.Printf("collaboration_prepare run_id=%s initial_agent_id=%s requested_agent_id=%q", run.ID, agent.ID, strings.TrimSpace(agentID))
	return PreparedCollaborationRun{WorkerAgent: agent, Run: run}, nil
}

func (r *Runtime) RunCollaboration(ctx context.Context, prepared PreparedCollaborationRun, task string) (<-chan domain.RunEvent, <-chan error) {
	events := make(chan domain.RunEvent)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)
		executionCtx, releaseCancellation := r.bindRunCancellation(ctx, prepared.Run.ID)
		defer releaseCancellation()

		_, err := r.runCollaborationStep(executionCtx, events, prepared, "planner", "", plannerPrompt(), task, userInputRetrievalQuery(task))
		if err != nil {
			errs <- err
			return
		}
		waiting, err := r.store.UpdateRunStatus(prepared.Run.ID, domain.RunWaitingForUser, "")
		if err != nil {
			errs <- err
			return
		}
		r.publishRunLifecycle(executionCtx, waiting, domain.EventRunWaitingForUser, map[string]any{"status": waiting.Status})
	}()

	return events, errs
}

func (r *Runtime) ContinueCollaboration(ctx context.Context, runID string, plan string, requirements domain.AgentRoutingRequirements) (<-chan domain.RunEvent, <-chan error) {
	requirements = domain.NormalizeAgentRoutingRequirements(requirements)
	events := make(chan domain.RunEvent)
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
		executionCtx, releaseCancellation := r.bindRunCancellation(ctx, run.ID)
		defer releaseCancellation()

		restored, err := r.restoreRuntime(run)
		if err != nil {
			errs <- err
			return
		}
		if restored.mode != ChatModeMultiAgent {
			errs <- fmt.Errorf("run %s uses %q mode, not %q", run.ID, restored.mode, ChatModeMultiAgent)
			return
		}
		agents := restored.candidateAgents
		if len(agents) == 0 {
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
		_ = r.runEventSink().Publish(executionCtx, domain.RunEvent{
			Type: domain.EventRunProgress, RunID: run.ID, ConversationID: run.ConversationID,
			StageID: updatedPlan.ID, Payload: map[string]any{"kind": "plan_approved", "plan": updatedPlan.Output},
		})
		events <- liveStageEvent(updatedPlan)
		run, err = r.store.UpdateRunStatus(run.ID, domain.RunRunning, "")
		if err != nil {
			errs <- err
			return
		}
		r.publishRunLifecycle(executionCtx, run, domain.EventRunResumed, map[string]any{"status": run.Status})
		route, routeErr := r.routeWorkerAgent(executionCtx, run.ID, restored, agents, task, plan, requirements)
		routerInput := fmt.Sprintf("User task:\n%s\n\nApproved plan:\n%s\n\nRouting requirements:\n%s\n\nCandidate agents:\n%s", task, plan, formatRoutingRequirements(requirements), formatCandidateAgents(agents))
		routerStatus := domain.CollaborationStepCompleted
		routerError := ""
		if routeErr != nil {
			routerStatus = domain.CollaborationStepFailed
			routerError = routeErr.Error()
		}
		routerStep, err := r.store.CreateCollaborationStep(domain.CollaborationStep{
			RunID:          run.ID,
			ConversationID: run.ConversationID,
			Role:           "router",
			AgentID:        route.Agent.ID,
			Status:         routerStatus,
			Input:          routerInput,
			Output:         route.Output(),
			Error:          routerError,
		})
		if err != nil {
			errs <- err
			return
		}
		routerEventType := domain.EventStageCompleted
		if routeErr != nil {
			routerEventType = domain.EventStageFailed
		}
		if err := r.publishStage(executionCtx, routerStep, routerEventType); err != nil {
			errs <- err
			return
		}
		if err := r.publishAgentSelection(executionCtx, run, routerStep.ID, route); err != nil {
			errs <- err
			return
		}
		events <- liveStageEvent(routerStep)
		if routeErr != nil {
			errs <- routeErr
			return
		}
		run, err = r.store.UpdateRunAgent(run.ID, route.Agent.ID)
		if err != nil {
			errs <- err
			return
		}
		events <- liveRunEvent(run)
		log.Printf("collaboration_router_decision run_id=%s router_mode=%s selected_agent_id=%s selected_agent_name=%q score=%d confidence=%.2f reason=%q", run.ID, route.Mode, route.Agent.ID, route.Agent.Name, route.Score, route.Confidence, route.Reason)

		prepared := PreparedCollaborationRun{WorkerAgent: route.Agent, Run: run}
		workerInput := fmt.Sprintf("User task:\n%s\n\nPlanner output:\n%s\n\nRouter-selected worker:\n%s (%s)\nSelection reason: %s", task, plan, route.Agent.Name, route.Agent.ID, route.Reason)
		worker, err := r.runWorkerStage(executionCtx, events, prepared, restored.catalog, workerInput, userInputRetrievalQuery(task))
		if err != nil {
			errs <- err
			return
		}

		if err := r.finishCollaboration(executionCtx, events, prepared, task, plan, worker); err != nil {
			errs <- err
			return
		}
	}()

	return events, errs
}

func NormalizeChatMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case "", ChatModeSingle:
		return ChatModeSingle
	case ChatModeMultiAgent:
		return ChatModeMultiAgent
	case ChatModeAutonomous:
		return ChatModeAutonomous
	default:
		return ChatModeSingle
	}
}

func NormalizeRouterMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case RouterModeQuery:
		return RouterModeQuery
	default:
		return RouterModeAuto
	}
}

func IsAgentNotFound(err error) bool {
	return store.IsNotFound(err)
}
