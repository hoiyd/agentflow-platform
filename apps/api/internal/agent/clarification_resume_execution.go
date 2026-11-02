package agent

import (
	"context"
	"encoding/json"

	"agentflow-platform/apps/api/internal/agent/turn"
	"agentflow-platform/apps/api/internal/clarification"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/inference/provider"
)

func (r *Runtime) resumeClarificationHumanInput(ctx, executionCtx context.Context, run domain.Run, record domain.Clarification, restored restoredRuntime, release func(), events chan<- domain.RunEvent) error {
	steps, err := r.store.ListCollaborationSteps(run.ID)
	if err != nil {
		return err
	}
	step, err := r.store.UpdateCollaborationStep(record.StageID, domain.CollaborationStepCompleted, clarificationAnswerText(record), "")
	if err != nil {
		return err
	}
	if err = r.publishStage(executionCtx, step, domain.EventStageCompleted); err != nil {
		return err
	}
	prepared := PreparedCollaborationRun{WorkerAgent: restored.agent, Run: run}
	release()
	resumed, failures := r.runAutonomousFromState(ctx, prepared, firstAutonomousTask(steps), rebuildAutonomousState(steps, step))
	return forwardClarificationResume(resumed, failures, events)
}

func (r *Runtime) resumeClarificationTool(ctx, executionCtx context.Context, run domain.Run, record domain.Clarification, saved clarificationCheckpoint, plan clarificationResumePlan, release func(), events chan<- domain.RunEvent) error {
	request, err := r.restoreClarificationTurn(saved, record, plan.restored)
	if err != nil {
		return err
	}
	result := turn.Result{}
	if plan.completedStage != nil {
		result.Output = plan.completedStage.Output
	} else {
		result, err = r.turnEngine.Execute(executionCtx, request, func(item turn.Event) {
			forwardReasoning(events)(item)
			if plan.restored.mode == ChatModeSingle && item.Type == turn.EventModelDelta {
				event := liveDeltaEvent(run.ID, item.Delta)
				if item.Reset {
					event.Payload["reset"] = true
				}
				events <- event
			}
		})
		if err != nil {
			return err
		}
	}
	if plan.restored.mode == ChatModeSingle {
		return nil
	}
	step := domain.CollaborationStep{}
	if plan.completedStage != nil {
		step = *plan.completedStage
	} else {
		step, err = r.store.UpdateCollaborationStep(record.StageID, domain.CollaborationStepCompleted, result.Output, "")
		if err != nil {
			return err
		}
		if err = r.publishStage(executionCtx, step, domain.EventStageCompleted); err != nil {
			return err
		}
		events <- liveStageEvent(step)
	}
	if plan.restored.mode == ChatModeMultiAgent {
		waiting, err := r.transitionRun(run, domain.RunWaitingForUser, domain.EventRunWaitingForUser, "", map[string]any{"source": "plan_approval"})
		if err != nil {
			return err
		}
		events <- liveRunEvent(waiting)
		return nil
	}
	return r.resumeClarificationAutonomous(ctx, run, step, plan.restored, release, events)
}

func (r *Runtime) restoreClarificationTurn(saved clarificationCheckpoint, record domain.Clarification, restored restoredRuntime) (turn.Request, error) {
	if saved.Continuation == nil {
		return turn.Request{}, clarification.ErrInvalid
	}
	if err := saved.Continuation.RestoreSources(); err != nil {
		return turn.Request{}, err
	}
	answers, _ := json.Marshal(map[string]any{"questions": record.Questions, "answers": record.Answers, "clarification_id": record.ID})
	saved.Continuation.Messages = append(saved.Continuation.Messages, provider.Message{Role: "tool", ToolCallID: record.ToolCallID, Content: string(answers)})
	saved.Continuation.Sources = append(saved.Continuation.Sources, "tool_result")
	saved.Continuation.References = append(saved.Continuation.References, record.ToolCallID)
	request := saved.Request
	request.TurnID = ""
	request.Sink = r.runEventSink()
	request.Catalog = restored.catalog
	if request.Role == "planner" {
		var err error
		request.Catalog, err = r.clarificationCatalog(restored.catalog)
		if err != nil {
			return turn.Request{}, err
		}
	}
	request.Continuation, _ = json.Marshal(saved.Continuation)
	return request, nil
}

func (r *Runtime) resumeClarificationAutonomous(ctx context.Context, run domain.Run, step domain.CollaborationStep, restored restoredRuntime, release func(), events chan<- domain.RunEvent) error {
	steps, err := r.store.ListCollaborationSteps(run.ID)
	if err != nil {
		return err
	}
	state := rebuildRecoverableAutonomousState(steps, domain.CollaborationStep{})
	state.NextIter = step.Iteration
	state.OutputChars = 0
	for _, s := range steps {
		if s.Iteration < step.Iteration && s.Status == domain.CollaborationStepCompleted {
			state.OutputChars += len(s.Output)
		}
	}
	prepared := PreparedCollaborationRun{WorkerAgent: restored.agent, Run: run}
	release()
	loopCtx := context.WithValue(ctx, resumedIterationKey{}, step.Iteration)
	resumed, failures := r.runAutonomousFromState(loopCtx, prepared, firstAutonomousTask(steps), state)
	return forwardClarificationResume(resumed, failures, events)
}
