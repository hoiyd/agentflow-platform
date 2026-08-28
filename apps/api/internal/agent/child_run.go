package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/taskstate"
	"agentflow-platform/apps/api/internal/tools"
	turnpkg "agentflow-platform/apps/api/internal/turn"
)

func (r *Runtime) runDelegatedWorker(ctx context.Context, events chan<- domain.RunEvent, parent PreparedCollaborationRun, task string, reservation interface {
	Bind(string, context.CancelCauseFunc)
}) (string, error) {
	parentStep, err := r.store.CreateCollaborationStep(domain.CollaborationStep{
		RunID: parent.Run.ID, ConversationID: parent.Run.ConversationID, Role: "worker",
		AgentID: parent.WorkerAgent.ID, Status: domain.CollaborationStepRunning, Input: task,
	})
	if err != nil {
		return "", err
	}
	emitCollaborationEvent(events, liveStageEvent(parentStep))
	if err := r.publishStage(ctx, parentStep, domain.EventStageStarted); err != nil {
		return "", err
	}

	delegationID := runtimeID("delegation")
	parentTurnID := runtimeID("turn")
	snapshot, err := r.childRuntimeSnapshot(parent.Run, parent.WorkerAgent, delegationID, parentTurnID, parentStep.ID)
	if err != nil {
		return "", r.failDelegatedParentStep(ctx, events, parentStep, err)
	}
	childRun, relation, err := r.store.CreateChildRun(domain.ChildRunRequest{
		Delegation: domain.RunDelegation{
			ID: delegationID, ParentRunID: parent.Run.ID, ParentTurnID: parentTurnID,
			ParentStageID: parentStep.ID, AgentID: parent.WorkerAgent.ID, Depth: 1,
			Task: task, TimeoutMS: snapshot.Delegation.TimeoutMS,
		},
		RuntimeSnapshot: snapshot,
	})
	if err != nil {
		return "", r.failDelegatedParentStep(ctx, events, parentStep, err)
	}
	if err := r.publishDelegation(ctx, parent.Run, parentStep.ID, parentTurnID, domain.EventDelegationCreated, relation, nil); err != nil {
		return "", r.failDelegation(ctx, events, parentStep, childRun, relation, err)
	}

	return r.executeDelegatedChild(ctx, events, parent, parentStep, childRun, relation, reservation)
}

func (r *Runtime) executeDelegatedChild(ctx context.Context, events chan<- domain.RunEvent, parent PreparedCollaborationRun, parentStep domain.CollaborationStep, childRun domain.Run, relation domain.RunDelegation, reservation interface {
	Bind(string, context.CancelCauseFunc)
}) (string, error) {
	previousChildStatus := childRun.Status
	childRun, err := r.store.UpdateRunStatus(childRun.ID, domain.RunRunning, "")
	if err != nil {
		return "", r.failDelegation(ctx, events, parentStep, childRun, relation, err)
	}
	if previousChildStatus == domain.RunQueued {
		r.publishRunLifecycle(ctx, childRun, domain.EventRunCreated, map[string]any{"status": domain.RunQueued, "parent_run_id": parent.Run.ID, "delegation_id": relation.ID})
		r.publishRunLifecycle(ctx, childRun, domain.EventRunStarted, map[string]any{"status": childRun.Status, "parent_run_id": parent.Run.ID, "delegation_id": relation.ID})
	} else {
		r.publishRunLifecycle(ctx, childRun, domain.EventRunResumed, map[string]any{"status": childRun.Status, "parent_run_id": parent.Run.ID, "delegation_id": relation.ID})
	}
	relation, err = r.store.UpdateRunDelegation(relation.ID, domain.DelegationResult{Status: domain.DelegationRunning})
	if err != nil {
		return "", r.failDelegation(ctx, events, parentStep, childRun, relation, err)
	}
	if err := r.publishDelegation(ctx, parent.Run, parentStep.ID, relation.ParentTurnID, domain.EventDelegationStarted, relation, nil); err != nil {
		return "", r.failDelegation(ctx, events, parentStep, childRun, relation, err)
	}

	timeout := r.childRunLimits.Timeout
	if childRun.RuntimeSnapshot != nil && childRun.RuntimeSnapshot.Delegation != nil && childRun.RuntimeSnapshot.Delegation.TimeoutMS > 0 {
		timeout = time.Duration(childRun.RuntimeSnapshot.Delegation.TimeoutMS) * time.Millisecond
	}
	timeoutCtx, timeoutCancel := context.WithTimeout(ctx, timeout)
	defer timeoutCancel()
	childCtx, cancelChild := context.WithCancelCause(timeoutCtx)
	defer cancelChild(nil)
	reservation.Bind(childRun.ID, cancelChild)
	stopHeartbeat := r.startChildRunHeartbeat(childCtx, childRun.ID, cancelChild)
	defer stopHeartbeat()

	restored, err := r.restoreRuntime(childRun)
	if err == nil {
		_, err = r.runChildWorkerStep(childCtx, childRun, restored.agent, restored.catalog, relation.Task)
	}
	if err != nil {
		if cause := context.Cause(childCtx); cause != nil {
			err = cause
		}
		return "", r.failDelegation(ctx, events, parentStep, childRun, relation, err)
	}
	steps, err := r.store.ListCollaborationSteps(childRun.ID)
	if err != nil || len(steps) == 0 {
		if err == nil {
			err = errors.New("child worker produced no durable stage")
		}
		return "", r.failDelegation(ctx, events, parentStep, childRun, relation, err)
	}
	maxChars := r.childRunLimits.SummaryMaxChars
	if childRun.RuntimeSnapshot != nil && childRun.RuntimeSnapshot.Delegation != nil && childRun.RuntimeSnapshot.Delegation.SummaryMaxChars > 0 {
		maxChars = childRun.RuntimeSnapshot.Delegation.SummaryMaxChars
	}
	result := domain.CompletedDelegationResult(childRun.ID, steps[len(steps)-1], maxChars)
	childRun, err = r.store.UpdateRunStatus(childRun.ID, domain.RunCompleted, "")
	if err != nil {
		return "", r.failDelegation(ctx, events, parentStep, childRun, relation, err)
	}
	r.publishRunLifecycle(ctx, childRun, domain.EventRunCompleted, map[string]any{"status": childRun.Status, "parent_run_id": parent.Run.ID, "delegation_id": relation.ID})
	relation, err = r.store.UpdateRunDelegation(relation.ID, result)
	if err != nil {
		return "", r.failDelegatedParentStep(ctx, events, parentStep, err)
	}
	parentOutput := result.Summary + "\n\nChild trace: " + result.OutputRef
	parentStep, err = r.store.UpdateCollaborationStep(parentStep.ID, domain.CollaborationStepCompleted, parentOutput, "")
	if err != nil {
		return "", err
	}
	emitCollaborationEvent(events, liveStageEvent(parentStep))
	if err := r.publishStage(ctx, parentStep, domain.EventStageCompleted); err != nil {
		return "", err
	}
	if err := r.publishDelegation(ctx, parent.Run, parentStep.ID, relation.ParentTurnID, domain.EventDelegationCompleted, relation, nil); err != nil {
		return "", err
	}
	return parentOutput, nil
}

func (r *Runtime) startChildRunHeartbeat(ctx context.Context, runID string, cancel context.CancelCauseFunc) context.CancelFunc {
	heartbeatCtx, stop := context.WithCancel(ctx)
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				if _, err := r.store.UpdateRunHeartbeat(runID); err != nil {
					cancel(fmt.Errorf("update child run heartbeat: %w", err))
					return
				}
			}
		}
	}()
	return stop
}

func (r *Runtime) childRuntimeSnapshot(parent domain.Run, selected domain.Agent, delegationID, parentTurnID, parentStageID string) (domain.RuntimeSnapshot, error) {
	if err := validateRuntimeSnapshot(parent.RuntimeSnapshot); err != nil {
		return domain.RuntimeSnapshot{}, err
	}
	selectedSnapshot := domain.RuntimeAgentSnapshot{}
	for _, candidate := range parent.RuntimeSnapshot.CandidateAgents {
		if candidate.ID == selected.ID {
			selectedSnapshot = candidate
			break
		}
	}
	if selectedSnapshot.ID == "" {
		return domain.RuntimeSnapshot{}, errors.New("selected worker is absent from the frozen parent candidates")
	}
	allowedNames := map[string]bool{}
	for _, name := range selectedSnapshot.Tools {
		if name != taskstate.UpdateToolName {
			allowedNames[name] = true
		}
	}
	toolSnapshots := make([]domain.RuntimeToolSnapshot, 0, len(allowedNames))
	for _, frozen := range parent.RuntimeSnapshot.Tools {
		if allowedNames[frozen.Name] {
			toolSnapshots = append(toolSnapshots, frozen)
		}
	}
	selectedSnapshot.Tools = make([]string, 0, len(toolSnapshots))
	for _, frozen := range toolSnapshots {
		selectedSnapshot.Tools = append(selectedSnapshot.Tools, frozen.Name)
	}
	contextConfig := parent.RuntimeSnapshot.ContextAssembly
	contextConfig.HistoryRetrievalEnabled = false
	contextConfig.CompactionMode = "off"
	childPolicy := parent.RuntimeSnapshot.ChildRunPolicy
	if childPolicy == nil {
		limits := normalizeChildRunLimits(r.childRunLimits)
		childPolicy = &domain.RuntimeChildRunPolicy{
			MaxDepth: 1, TimeoutMS: limits.Timeout.Milliseconds(), SummaryMaxChars: limits.SummaryMaxChars,
			AgentDefinitionSource: "runtime_snapshot.candidate_agents", RunBudget: limits.RunBudget,
		}
	}
	return domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, Mode: ChatModeSingle,
		Agent: selectedSnapshot, Model: parent.RuntimeSnapshot.Model, Tools: toolSnapshots,
		ContextAssembly: contextConfig, RunBudget: cloneRunBudget(childPolicy.RunBudget),
		Delegation: &domain.RuntimeDelegation{
			DelegationID: delegationID, ParentRunID: parent.ID, ParentTurnID: parentTurnID,
			ParentStageID: parentStageID, Depth: 1, IsolatedContext: true,
			TimeoutMS: childPolicy.TimeoutMS, SummaryMaxChars: childPolicy.SummaryMaxChars,
		},
		CreatedAt: time.Now().UTC(),
	}, nil
}

func (r *Runtime) runChildWorkerStep(ctx context.Context, run domain.Run, agent domain.Agent, catalog *tools.Catalog, input string) (string, error) {
	step, err := r.store.CreateCollaborationStep(domain.CollaborationStep{
		RunID: run.ID, ConversationID: run.ConversationID, Role: "worker", AgentID: agent.ID,
		Status: domain.CollaborationStepRunning, Input: input,
	})
	if err != nil {
		return "", err
	}
	r.publishLive(liveStageEvent(step))
	if err := r.publishStage(ctx, step, domain.EventStageStarted); err != nil {
		return "", err
	}
	retrievedMemories, retrievedChunks := r.retrieveContext(ctx, run.ID, input, agent.MemoryEnabled, agent.RetrievalEnabled, map[string]any{
		"executor": agent.Executor, "framework": "agentflow-native", "delegated": true,
	})
	result, err := r.turnEngine.Execute(ctx, turnpkg.Request{
		RunID: run.ID, StepID: step.ID, ConversationID: run.ConversationID,
		Agent: agent, Role: "worker", SystemPrompt: workerPrompt(agent), Input: input,
		ExecutorKind: agent.Executor, Catalog: catalog,
		Context: turnpkg.Context{Memories: retrievedMemories, Chunks: retrievedChunks}, Sink: r.runEventSink(),
	}, nil)
	if err != nil {
		failed, updateErr := r.store.UpdateCollaborationStep(step.ID, domain.CollaborationStepFailed, "", err.Error())
		if updateErr == nil {
			r.publishLive(liveStageEvent(failed))
			_ = r.publishStage(ctx, failed, domain.EventStageFailed)
		}
		return "", err
	}
	completed, err := r.store.UpdateCollaborationStep(step.ID, domain.CollaborationStepCompleted, result.Output, "")
	if err != nil {
		return "", err
	}
	r.publishLive(liveStageEvent(completed))
	if err := r.publishStage(ctx, completed, domain.EventStageCompleted); err != nil {
		return "", err
	}
	return result.Output, nil
}

func (r *Runtime) failDelegation(ctx context.Context, events chan<- domain.RunEvent, parentStep domain.CollaborationStep, childRun domain.Run, relation domain.RunDelegation, cause error) error {
	status, eventType, runStatus := domain.DelegationFailed, domain.EventDelegationFailed, domain.RunFailed
	if errors.Is(cause, context.Canceled) {
		status, eventType, runStatus = domain.DelegationCanceled, domain.EventDelegationCanceled, domain.RunCanceled
	}
	updatedRun, updateRunErr := r.store.UpdateRunStatus(childRun.ID, runStatus, cause.Error())
	if updateRunErr == nil {
		r.publishRunLifecycle(ctx, updatedRun, map[domain.RunStatus]domain.RunEventType{domain.RunFailed: domain.EventRunFailed, domain.RunCanceled: domain.EventRunCanceled}[runStatus], failure.Merge(map[string]any{"status": runStatus}, cause))
	}
	updated, _ := r.store.UpdateRunDelegation(relation.ID, domain.DelegationResult{Status: status, Error: cause.Error()})
	_ = r.publishDelegation(ctx, parentStepRun(parentStep, relation), parentStep.ID, relation.ParentTurnID, eventType, updated, cause)
	return r.failDelegatedParentStep(ctx, events, parentStep, cause)
}

func parentStepRun(step domain.CollaborationStep, relation domain.RunDelegation) domain.Run {
	return domain.Run{ID: relation.ParentRunID, ConversationID: step.ConversationID}
}

func (r *Runtime) failDelegatedParentStep(ctx context.Context, events chan<- domain.RunEvent, step domain.CollaborationStep, cause error) error {
	failed, err := r.store.UpdateCollaborationStep(step.ID, domain.CollaborationStepFailed, "", cause.Error())
	if err == nil {
		emitCollaborationEvent(events, liveStageEvent(failed))
		_ = r.publishStage(ctx, failed, domain.EventStageFailed)
	}
	return cause
}

func (r *Runtime) publishDelegation(ctx context.Context, parent domain.Run, stageID, turnID string, eventType domain.RunEventType, item domain.RunDelegation, cause error) error {
	payload := map[string]any{
		"delegation_id": item.ID, "parent_run_id": item.ParentRunID, "child_run_id": item.ChildRunID,
		"agent_id": item.AgentID, "depth": item.Depth, "status": item.Status,
		"block_reason": item.BlockReason,
		"summary":      item.Summary, "output_ref": item.OutputRef, "output_hash": item.OutputHash,
		"output_bytes": item.OutputBytes, "summary_truncated": item.SummaryTruncated,
	}
	return r.runEventSink().Publish(ctx, domain.RunEvent{
		Type: eventType, RunID: parent.ID, ConversationID: parent.ConversationID,
		StageID: stageID, TurnID: turnID, Payload: failure.Merge(payload, cause),
	})
}

func emitCollaborationEvent(events chan<- domain.RunEvent, event domain.RunEvent) {
	if events != nil {
		events <- event
	}
}

func runtimeID(prefix string) string {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err == nil {
		return prefix + "_" + hex.EncodeToString(buffer)
	}
	return fmt.Sprintf("%s_fallback", prefix)
}
