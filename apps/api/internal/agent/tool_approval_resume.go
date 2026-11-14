package agent

import (
	"context"
	"encoding/json"
	"errors"

	"agentflow-platform/apps/api/internal/agent/turn"
	"agentflow-platform/apps/api/internal/approval"
	"agentflow-platform/apps/api/internal/checkpoint"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/tool"
)

func (r *Runtime) ResumeToolApproval(ctx context.Context, owner, workspace, runID, id string) (<-chan domain.RunEvent, <-chan error) {
	events, errs := make(chan domain.RunEvent), make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		if err := r.resumeToolApproval(ctx, owner, workspace, runID, id, events); err != nil {
			errs <- err
		}
	}()
	return events, errs
}

func (r *Runtime) resumeToolApproval(ctx context.Context, owner, workspace, runID, id string, events chan<- domain.RunEvent) (resultErr error) {
	if r.toolApprovals == nil {
		return approval.ErrConflict
	}
	run, ok, err := r.store.GetRun(runID)
	if err != nil {
		return err
	}
	if !ok || run.WorkspaceID != workspace {
		return approval.ErrConflict
	}
	restored, err := r.restoreRuntime(run)
	if err != nil {
		return err
	}
	items, err := r.toolApprovals.ListToolApprovals(ctx, owner, workspace, runID)
	if err != nil {
		return err
	}
	if len(items) == 0 || items[len(items)-1].ID != id {
		return approval.ErrConflict
	}
	record := items[len(items)-1]
	agent := restored.agent
	if restored.mode == ChatModeMultiAgent {
		steps, err := r.store.ListCollaborationSteps(runID)
		if err != nil {
			return err
		}
		for _, step := range steps {
			if step.ID == record.StageID && step.AgentID != "" {
				agent, ok = findAgentByID(restored.candidateAgents, step.AgentID)
				if !ok {
					return approval.ErrConflict
				}
			}
		}
	}
	call := tool.ExecutionRequest{RunID: runID, StageID: record.StageID, TurnID: record.TurnID, CallID: record.ToolCallID, Tool: record.Tool, Arguments: record.Arguments, DefinitionRevision: record.DefinitionRevision, ArgumentsHash: record.ArgumentsHash}
	if err = r.validateToolApproval(ctx, run, record, agent, call); err != nil {
		return err
	}
	completedOutput, completed, err := r.completedToolApproval(run, record)
	if err != nil {
		return err
	}
	claimed, err := r.toolApprovals.ClaimToolApproval(ctx, owner, workspace, runID, id)
	if err != nil {
		return err
	}
	defer func() {
		if resultErr != nil && !turn.IsWaiting(resultErr) && record.StageID != "" {
			resultErr = errors.Join(resultErr, r.failResumedClarification(ctx, runID, record.StageID, resultErr))
		}
	}()
	var saved toolApprovalCheckpoint
	if json.Unmarshal(claimed.Checkpoint, &saved) != nil || saved.Version != 1 || saved.Request.RunID != runID || saved.Request.ConversationID != run.ConversationID || saved.Request.StepID != record.StageID || saved.Request.TurnID != record.TurnID || saved.Request.Agent.ID != agent.ID || saved.Continuation.Approval == nil || saved.Continuation.PendingCallID != record.ToolCallID {
		return checkpoint.ErrCheckpointStale
	}
	if err = saved.Continuation.RestoreSources(); err != nil {
		return err
	}
	if err = r.validateToolApproval(ctx, run, record, agent, *saved.Continuation.Approval); err != nil {
		return err
	}
	// Validate the retained provider protocol too: the resumed observation must
	// describe the exact call reviewed by the user, not a different assistant call.
	last := saved.Continuation.Messages[len(saved.Continuation.Messages)-1]
	protocolCall := call
	protocolCall.Arguments = json.RawMessage(last.ToolCalls[0].Function.Arguments)
	if err = r.validateToolApproval(ctx, run, record, agent, protocolCall); err != nil {
		return approval.ErrConflict
	}
	saved.Continuation.Approval.ApprovalID = id
	request := saved.Request
	request.Agent = agent
	request.Catalog = restored.catalog
	if restored.mode == ChatModeMultiAgent {
		request.Catalog, err = catalogForAgent(restored.catalog, agent)
		if err != nil {
			return err
		}
	}
	if request.Role == "planner" {
		request.Catalog, err = r.clarificationCatalog(restored.catalog)
		if err != nil {
			return err
		}
	}
	request.Sink = r.runEventSink()
	request.ApprovalID = id
	request.Continuation, _ = json.Marshal(saved.Continuation)
	// ApprovalID is deliberately not JSON. Reinstall it only in the trusted
	// decoder via the ephemeral request capability, never from a model field.
	request.ConsumeApproval = func(ctx context.Context, attempt tool.ExecutionRequest) error {
		current, ok, err := r.store.GetRun(runID)
		if err != nil {
			return err
		}
		if !ok || current.Status != domain.RunRunning {
			return approval.ErrConflict
		}
		if err = r.validateToolApproval(ctx, current, record, agent, attempt); err != nil {
			return err
		}
		_, err = r.toolApprovals.ConsumeToolApproval(ctx, owner, workspace, id, claimed.Claim, approval.BindingHash(record))
		return err
	}
	executionCtx, release := r.bindRunCancellation(ctx, runID)
	defer release()
	run, _, err = r.store.GetRun(runID)
	if err != nil {
		return err
	}
	if run.Status != domain.RunRunning {
		return context.Canceled
	}
	events <- liveRunEvent(run)
	result := turn.Result{Output: completedOutput}
	if completed {
		call.ApprovalID = id
		if err = request.ConsumeApproval(executionCtx, call); err != nil {
			return err
		}
	}
	if !completed {
		result, err = r.turnEngine.Execute(executionCtx, request, func(item turn.Event) {
			forwardReasoning(events)(item)
			if restored.mode == ChatModeSingle && item.Type == turn.EventModelDelta {
				event := liveDeltaEvent(runID, item.Delta)
				if item.Reset {
					event.Payload["reset"] = true
				}
				events <- event
			}
		})
	}
	if err != nil {
		return err
	}
	if restored.mode == ChatModeSingle {
		if completed {
			r.emitFinalDeltas(executionCtx, runID, result.Output, events)
		}
		return nil
	}
	steps, err := r.store.ListCollaborationSteps(runID)
	if err != nil {
		return err
	}
	var step domain.CollaborationStep
	for _, item := range steps {
		if item.ID == record.StageID {
			step = item
		}
	}
	if !completed {
		step, err = r.store.UpdateCollaborationStep(record.StageID, domain.CollaborationStepCompleted, result.Output, "")
		if err != nil {
			return err
		}
		if err = r.publishStage(executionCtx, step, domain.EventStageCompleted); err != nil {
			return err
		}
		events <- liveStageEvent(step)
	}
	if restored.mode == ChatModeAutonomous {
		return r.resumeClarificationAutonomous(ctx, run, step, restored, release, events)
	}
	if step.Role == "planner" {
		waiting, err := r.transitionRun(run, domain.RunWaitingForUser, domain.EventRunWaitingForUser, "", map[string]any{"source": "plan_approval"})
		if err != nil {
			return err
		}
		events <- liveRunEvent(waiting)
		return nil
	}
	steps, err = r.store.ListCollaborationSteps(runID)
	if err != nil {
		return err
	}
	planner, found := latestCompletedCollaborationStep(steps, "planner")
	if !found {
		return approval.ErrConflict
	}
	router, found := latestCompletedCollaborationStep(steps, "router")
	if !found {
		return approval.ErrConflict
	}
	reason, err := r.persistedSelectionReason(runID, router)
	if err != nil {
		return err
	}
	return r.runCollaborationTail(executionCtx, events, PreparedCollaborationRun{WorkerAgent: agent, Run: run}, restored.catalog, planner.Input, planner.Output, reason, steps)
}
