package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"agentflow-platform/apps/api/internal/agent/toolloop"
	"agentflow-platform/apps/api/internal/agent/turn"
	"agentflow-platform/apps/api/internal/checkpoint"
	"agentflow-platform/apps/api/internal/clarification"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/tool"
)

type clarificationCheckpoint struct {
	Version      int
	SnapshotHash string
	Request      turn.Request
	Continuation *toolloop.Continuation
	HumanInput   bool
}

func decodeContinuation(data json.RawMessage) (*toolloop.Continuation, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var c toolloop.Continuation
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.Identity == "" || c.NextRound < 2 || len(c.Messages) != len(c.Sources) || len(c.Messages) != len(c.References) {
		return nil, clarification.ErrInvalid
	}
	for i := range c.Messages {
		c.Messages[i].Source = c.Sources[i]
		c.Messages[i].ReferenceID = c.References[i]
	}
	return &c, nil
}

func (r *Runtime) clarificationBoundary(request turn.Request) func(context.Context, []domain.ClarificationQuestion, toolloop.Continuation) error {
	if r.clarifications == nil || request.Context.Isolated || request.Role != "" && request.Role != "act" && request.Role != "planner" {
		return nil
	}
	return func(ctx context.Context, questions []domain.ClarificationQuestion, c toolloop.Continuation) error {
		run, ok, err := r.store.GetRun(request.RunID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrRuntimeSnapshotUnavailable
		}
		hash, _, err := checkpoint.RuntimeHashes(run.RuntimeSnapshot)
		if err != nil {
			return err
		}
		frozen := request
		frozen.Catalog = nil
		frozen.Sink = nil
		frozen.Continuation = nil
		data, err := json.Marshal(clarificationCheckpoint{Version: 1, SnapshotHash: hash, Request: frozen, Continuation: &c})
		if err != nil {
			return err
		}
		_, err = r.clarifications.SuspendClarification(ctx, domain.Clarification{RunID: request.RunID, ConversationID: request.ConversationID, StageID: request.StepID, TurnID: request.TurnID, ToolCallID: c.PendingCallID, Questions: questions}, data)
		if errors.Is(err, clarification.ErrConflict) {
			current, found, loadErr := r.store.GetRun(request.RunID)
			if loadErr == nil && found && (current.Status == domain.RunCanceling || current.Status == domain.RunCanceled) {
				return context.Canceled
			}
		}
		return err
	}
}

// ResumeClarification is called only after application admission. The owner
// is authenticated; the model cannot supply or replace it.
func (r *Runtime) ResumeClarification(ctx context.Context, owner, workspace, runID, id string) (<-chan domain.RunEvent, <-chan error) {
	events := make(chan domain.RunEvent)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		var resumeErr error
		stageID := ""
		fail := func(err error) { resumeErr = err }
		defer func() {
			if resumeErr != nil {
				if !errors.Is(resumeErr, clarification.Pending) && stageID != "" {
					resumeErr = errors.Join(resumeErr, r.failResumedClarification(ctx, runID, stageID, resumeErr))
				}
				errs <- resumeErr
			}
		}()
		if r.clarifications == nil {
			fail(clarification.ErrConflict)
			return
		}
		run, ok, err := r.store.GetRun(runID)
		if err != nil {
			fail(err)
			return
		}
		if !ok || run.WorkspaceID != workspace {
			fail(clarification.ErrConflict)
			return
		}
		restored, err := r.restoreRuntime(run)
		if err != nil {
			fail(err)
			return
		}
		// Validate the existing frozen capabilities before accepting execution.
		effects, err := r.store.ListToolEffects(runID)
		if err != nil {
			fail(err)
			return
		}
		for _, effect := range effects {
			if domain.ToolEffectRequiresReconciliation(effect.Status) {
				fail(checkpoint.ErrNeedsReconciliation)
				return
			}
		}
		records, err := r.clarifications.ListClarifications(ctx, owner, workspace, runID)
		if err != nil {
			fail(err)
			return
		}
		if len(records) == 0 || records[len(records)-1].ID != id {
			fail(clarification.ErrConflict)
			return
		}
		previous := records[len(records)-1]
		var completedStep *domain.CollaborationStep
		replayOutput := ""
		reuseOutput := false
		if run.Status == domain.RunFailedRecoverable && previous.Status == "applied" {
			steps, err := r.store.ListCollaborationSteps(runID)
			if err != nil {
				fail(err)
				return
			}
			for i := range steps {
				if steps[i].ID == previous.StageID && steps[i].Status == domain.CollaborationStepCompleted {
					completedStep = &steps[i]
				}
			}
			// A completed Planner with no Router is still the plan-approval boundary.
			routerCommitted := false
			for _, step := range steps {
				routerCommitted = routerCommitted || step.Role == "router" && step.Status == domain.CollaborationStepCompleted
			}
			if restored.mode == ChatModeMultiAgent && completedStep != nil && routerCommitted {
				resumed, failures := r.ResumeRecoverableCollaboration(ctx, runID)
				for item := range resumed {
					events <- item
				}
				if err := <-failures; err != nil {
					fail(err)
				}
				return
			}
			if completedStep == nil {
				for _, effect := range effects {
					if effect.CreatedAt.After(previous.CreatedAt) && effect.Status == domain.ToolEffectCommitted {
						fail(checkpoint.ErrNeedsReconciliation)
						return
					}
				}
			}
			if restored.mode == ChatModeSingle {
				items, err := r.store.ListRunEvents(runID)
				if err != nil {
					fail(err)
					return
				}
				resumedAt := int64(0)
				for _, item := range items {
					if item.Type == domain.EventRunResumed && item.Payload["kind"] == "clarification" {
						data, _ := json.Marshal(item.Payload["clarification"])
						var q domain.Clarification
						json.Unmarshal(data, &q)
						if q.ID == id {
							resumedAt = item.Sequence
						}
					}
					if resumedAt > 0 && item.Sequence > resumedAt && item.Type == domain.EventTurnCompleted && item.Payload["stop_reason"] == "completed" {
						replayOutput, _ = item.Payload["output"].(string)
						reuseOutput = true
					}
				}
			}
		}
		committed, err := r.clarifications.ClaimClarification(ctx, owner, workspace, runID, id)
		if err != nil {
			fail(err)
			return
		}
		record, data := committed.Clarification, committed.Checkpoint
		stageID = record.StageID
		var saved clarificationCheckpoint
		if err = json.Unmarshal(data, &saved); err != nil {
			fail(err)
			return
		}
		hash, _, err := checkpoint.RuntimeHashes(run.RuntimeSnapshot)
		if err != nil || saved.Version != 1 || saved.SnapshotHash != hash || saved.Request.RunID != runID || saved.Request.ConversationID != run.ConversationID || saved.Request.StepID != record.StageID || saved.Request.TurnID != record.TurnID {
			fail(checkpoint.ErrCheckpointStale)
			return
		}
		if !saved.HumanInput && saved.Continuation != nil && saved.Continuation.PendingCallID != record.ToolCallID {
			fail(checkpoint.ErrCheckpointStale)
			return
		}
		executionCtx, release := r.bindRunCancellation(ctx, runID)
		defer release()
		run, _, err = r.store.GetRun(runID)
		if err != nil {
			fail(err)
			return
		}
		if run.Status != domain.RunRunning {
			fail(context.Canceled)
			return
		}
		events <- liveRunEvent(run)
		if reuseOutput {
			r.emitFinalDeltas(executionCtx, runID, replayOutput, events)
			return
		}
		if saved.HumanInput {
			steps, err := r.store.ListCollaborationSteps(runID)
			if err != nil {
				fail(err)
				return
			}
			answer := clarificationAnswerText(record)
			step, err := r.store.UpdateCollaborationStep(record.StageID, domain.CollaborationStepCompleted, answer, "")
			if err != nil {
				fail(err)
				return
			}
			if err = r.publishStage(executionCtx, step, domain.EventStageCompleted); err != nil {
				fail(err)
				return
			}
			prepared := PreparedCollaborationRun{WorkerAgent: restored.agent, Run: run}
			release()
			resumed, failures := r.runAutonomousFromState(ctx, prepared, firstAutonomousTask(steps), rebuildAutonomousState(steps, step))
			for item := range resumed {
				events <- item
			}
			if err := <-failures; err != nil {
				fail(err)
			}
			return
		}
		if saved.Continuation == nil {
			fail(clarification.ErrInvalid)
			return
		}
		if err = saved.Continuation.RestoreSources(); err != nil {
			fail(err)
			return
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
			request.Catalog, err = r.clarificationCatalog(restored.catalog)
			if err != nil {
				fail(err)
				return
			}
		}
		request.Continuation, _ = json.Marshal(saved.Continuation)
		result := turn.Result{}
		if completedStep != nil {
			result.Output = completedStep.Output
		} else {
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
			if err != nil {
				fail(err)
				return
			}
		}
		if restored.mode == ChatModeSingle {
			return
		}
		step := domain.CollaborationStep{}
		if completedStep != nil {
			step = *completedStep
		} else {
			step, err = r.store.UpdateCollaborationStep(record.StageID, domain.CollaborationStepCompleted, result.Output, "")
			if err != nil {
				fail(err)
				return
			}
			if err = r.publishStage(executionCtx, step, domain.EventStageCompleted); err != nil {
				fail(err)
				return
			}
			events <- liveStageEvent(step)
		}
		if restored.mode == ChatModeMultiAgent {
			waiting, err := r.transitionRun(run, domain.RunWaitingForUser, domain.EventRunWaitingForUser, "", map[string]any{"source": "plan_approval"})
			if err != nil {
				fail(err)
				return
			}
			events <- liveRunEvent(waiting)
			return
		}
		steps, err := r.store.ListCollaborationSteps(runID)
		if err != nil {
			fail(err)
			return
		}
		prepared := PreparedCollaborationRun{WorkerAgent: restored.agent, Run: run}
		iteration := step.Iteration
		state := rebuildRecoverableAutonomousState(steps, domain.CollaborationStep{})
		state.NextIter = iteration
		state.OutputChars = 0
		for _, s := range steps {
			if s.Iteration < iteration && s.Status == domain.CollaborationStepCompleted {
				state.OutputChars += len(s.Output)
			}
		}
		release()
		loopCtx := context.WithValue(ctx, resumedIterationKey{}, iteration)
		resumed, failures := r.runAutonomousFromState(loopCtx, prepared, firstAutonomousTask(steps), state)
		for item := range resumed {
			events <- item
		}
		if err := <-failures; err != nil {
			fail(err)
		}
	}()
	return events, errs
}

type resumedIterationKey struct{}

func clarificationAnswerText(c domain.Clarification) string {
	parts := []string{}
	for _, q := range c.Questions {
		parts = append(parts, q.Question+"\n"+c.Answers[q.ID])
	}
	return strings.Join(parts, "\n\n")
}

// Explicit input handoff does not grant Worker any parent Tool authority.
func (r *Runtime) taskWithClarifications(runID, task string) (string, error) {
	items, err := r.store.ListRunEvents(runID)
	if err != nil {
		return "", err
	}
	return clarification.AddAnswers(task, items)
}

func (r *Runtime) clarificationCatalog(catalog *tool.Catalog) (*tool.Catalog, error) {
	binding, ok := catalog.ResolveReady(clarification.ToolName)
	if !ok {
		return tool.NewCatalog()
	}
	return tool.NewCatalogWithPolicy(catalog.SecurityPolicy(), binding)
}
