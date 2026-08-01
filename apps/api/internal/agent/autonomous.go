package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	turnpkg "agentflow-platform/apps/api/internal/turn"
)

var errRunCanceled = errors.New("run canceled")

type autonomousDecision struct {
	Decision    string `json:"decision"`
	Reason      string `json:"reason"`
	Question    string `json:"question"`
	FinalAnswer string `json:"final_answer"`
	ValidJSON   bool   `json:"-"`
}

type autonomousState struct {
	StartedAt   time.Time
	OutputChars int
	ToolCalls   int
	State       string
	LastAct     string
	LastReview  string
	NextIter    int
}

type humanInputNeed struct {
	Needed   bool
	Source   string
	Evidence string
	Question string
}

func (r *Runtime) PrepareAutonomousRun(ctx context.Context, agentID string, conversationID string) (PreparedCollaborationRun, error) {
	return r.PrepareAutonomousRunWithContract(ctx, agentID, conversationID, nil)
}

func (r *Runtime) PrepareAutonomousRunWithContract(ctx context.Context, agentID string, conversationID string, contract *domain.CompletionContract) (PreparedCollaborationRun, error) {
	agent, err := r.resolveAgent(agentID)
	if err != nil {
		return PreparedCollaborationRun{}, err
	}

	snapshot, err := r.captureRuntimeSnapshot(ChatModeAutonomous, agent, nil, "")
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
	log.Printf("autonomous_prepare run_id=%s agent_id=%s max_iterations=%d max_runtime=%s max_output_chars=%d max_tool_calls=%d", run.ID, agent.ID, r.autonomousLimits.MaxIterations, r.autonomousLimits.MaxRuntime, r.autonomousLimits.MaxOutputChars, r.autonomousLimits.MaxToolCalls)
	return PreparedCollaborationRun{WorkerAgent: agent, Run: run}, nil
}

func (r *Runtime) RunAutonomous(ctx context.Context, prepared PreparedCollaborationRun, task string) (<-chan domain.RunEvent, <-chan error) {
	return r.runAutonomousFromState(ctx, prepared, task, autonomousState{
		StartedAt: time.Now().UTC(),
		State:     "No prior autonomous work.",
		NextIter:  1,
	})
}

func (r *Runtime) ResumeAutonomous(ctx context.Context, runID string, userInput string) (<-chan domain.RunEvent, <-chan error) {
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
			errs <- fmt.Errorf("run not found")
			return
		}
		if run.Status != domain.RunWaitingForUser {
			errs <- fmt.Errorf("run is not waiting for user input")
			return
		}

		steps, err := r.store.ListCollaborationSteps(run.ID)
		if err != nil {
			errs <- err
			return
		}
		checkpoint := latestHumanInputStep(steps)
		if checkpoint == nil || checkpoint.Status != domain.CollaborationStepRunning {
			errs <- fmt.Errorf("human input checkpoint not found")
			return
		}
		userInput = strings.TrimSpace(userInput)
		if userInput == "" {
			errs <- fmt.Errorf("user input is required")
			return
		}

		restored, err := r.restoreRuntime(run)
		if err != nil {
			errs <- err
			return
		}
		if restored.mode != ChatModeAutonomous {
			errs <- fmt.Errorf("run %s uses %q mode, not %q", run.ID, restored.mode, ChatModeAutonomous)
			return
		}

		completedCheckpoint, err := r.store.UpdateCollaborationStep(checkpoint.ID, domain.CollaborationStepCompleted, userInput, "")
		if err != nil {
			errs <- err
			return
		}
		run, err = r.store.UpdateRunStatus(run.ID, domain.RunRunning, "")
		if err != nil {
			errs <- err
			return
		}
		log.Printf("autonomous_resume run_id=%s checkpoint_id=%s input_len=%d", run.ID, checkpoint.ID, len(userInput))
		r.publishRunLifecycle(ctx, run, domain.EventRunResumed, map[string]any{"status": run.Status})
		r.publishStage(ctx, completedCheckpoint, domain.EventStageCompleted)
		events <- liveRunEvent(run)
		events <- liveStageEvent(completedCheckpoint)

		state := rebuildAutonomousState(steps, completedCheckpoint)
		prepared := PreparedCollaborationRun{WorkerAgent: restored.agent, Run: run}
		resumedEvents, resumedErrs := r.runAutonomousFromState(ctx, prepared, firstAutonomousTask(steps), state)
		for event := range resumedEvents {
			events <- event
		}
		if err := <-resumedErrs; err != nil {
			errs <- err
		}
	}()
	return events, errs
}

func (r *Runtime) ResumeRecoverableAutonomous(ctx context.Context, runID string, recoveryNote string) (<-chan domain.RunEvent, <-chan error) {
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
			errs <- fmt.Errorf("run not found")
			return
		}
		if run.Status != domain.RunFailedRecoverable {
			errs <- fmt.Errorf("run is not recoverable")
			return
		}

		steps, err := r.store.ListCollaborationSteps(run.ID)
		if err != nil {
			errs <- err
			return
		}
		restored, err := r.restoreRuntime(run)
		if err != nil {
			errs <- err
			return
		}
		if restored.mode != ChatModeAutonomous {
			errs <- fmt.Errorf("run %s uses %q mode, not %q", run.ID, restored.mode, ChatModeAutonomous)
			return
		}

		recoveryStep, err := r.store.CreateCollaborationStep(domain.CollaborationStep{
			RunID:          run.ID,
			ConversationID: run.ConversationID,
			Role:           "recovery",
			AgentID:        run.AgentID,
			Status:         domain.CollaborationStepCompleted,
			Iteration:      nextAutonomousIteration(steps),
			Input:          "Resume failed recoverable autonomous run.",
			Output:         strings.TrimSpace(recoveryNote),
		})
		if err != nil {
			errs <- err
			return
		}
		run, err = r.store.UpdateRunStatus(run.ID, domain.RunRunning, "")
		if err != nil {
			errs <- err
			return
		}
		log.Printf("autonomous_recovery_resume run_id=%s recovery_step_id=%s note_len=%d", run.ID, recoveryStep.ID, len(recoveryNote))
		r.publishRunLifecycle(ctx, run, domain.EventRunResumed, map[string]any{"status": run.Status})
		r.publishStage(ctx, recoveryStep, domain.EventStageCompleted)
		events <- liveRunEvent(run)
		events <- liveStageEvent(recoveryStep)

		state := rebuildRecoverableAutonomousState(steps, recoveryStep)
		prepared := PreparedCollaborationRun{WorkerAgent: restored.agent, Run: run}
		resumedEvents, resumedErrs := r.runAutonomousFromState(ctx, prepared, firstAutonomousTask(steps), state)
		for event := range resumedEvents {
			events <- event
		}
		if err := <-resumedErrs; err != nil {
			errs <- err
		}
	}()
	return events, errs
}

func (r *Runtime) runAutonomousFromState(ctx context.Context, prepared PreparedCollaborationRun, task string, initial autonomousState) (<-chan domain.RunEvent, <-chan error) {
	events := make(chan domain.RunEvent)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		limits, enforceLegacyResourceLimits, err := r.limitsForRun(prepared.Run.ID)
		if err != nil {
			errs <- err
			return
		}
		startedAt := initial.StartedAt
		if startedAt.IsZero() {
			startedAt = time.Now().UTC()
		}
		outputChars := initial.OutputChars
		toolCalls := initial.ToolCalls
		state := initial.State
		if strings.TrimSpace(state) == "" {
			state = "No prior autonomous work."
		}
		lastAct := initial.LastAct
		lastReview := initial.LastReview
		nextIter := initial.NextIter
		if nextIter <= 0 {
			nextIter = 1
		}
		if nextIter > limits.MaxIterations {
			reason := "recovered after max_iterations reached"
			r.emitAutonomousProgress(events, prepared.Run.ID, limits, startedAt, limits.MaxIterations, outputChars, toolCalls, reason)
			if err := r.finishAutonomous(ctx, events, prepared, limits.MaxIterations, task, lastAct, lastReview, reason); err != nil {
				errs <- err
			}
			return
		}

		for iteration := nextIter; iteration <= limits.MaxIterations; iteration++ {
			if err := r.heartbeatRun(prepared.Run.ID); err != nil {
				errs <- err
				return
			}
			r.emitAutonomousProgress(events, prepared.Run.ID, limits, startedAt, iteration, outputChars, toolCalls, "")
			if stopped, err := r.stopIfCanceled(events, prepared.Run.ID); stopped || err != nil {
				if err != errRunCanceled {
					errs <- err
				}
				return
			}
			if reason := limitStopReason(limits, startedAt, outputChars, toolCalls, enforceLegacyResourceLimits); reason != "" {
				r.emitAutonomousProgress(events, prepared.Run.ID, limits, startedAt, iteration, outputChars, toolCalls, reason)
				if err := r.finishAutonomous(ctx, events, prepared, iteration, task, lastAct, lastReview, reason); err != nil {
					errs <- err
				}
				return
			}

			log.Printf("autonomous_iteration_start run_id=%s iteration=%d output_chars=%d tool_calls=%d", prepared.Run.ID, iteration, outputChars, toolCalls)

			observe, err := r.runAutonomousStep(ctx, events, prepared, iteration, "observe", autonomousObservePrompt(), autonomousObserveInput(task, state, limits, startedAt, outputChars, toolCalls))
			if err != nil {
				if err == errRunCanceled {
					return
				}
				errs <- err
				return
			}
			outputChars += len(observe)
			r.emitAutonomousProgress(events, prepared.Run.ID, limits, startedAt, iteration, outputChars, toolCalls, "")
			if stopped, err := r.stopIfCanceled(events, prepared.Run.ID); stopped || err != nil {
				if err != errRunCanceled {
					errs <- err
				}
				return
			}

			plan, err := r.runAutonomousStep(ctx, events, prepared, iteration, "plan", autonomousPlanPrompt(), autonomousPlanInput(task, observe, state))
			if err != nil {
				if err == errRunCanceled {
					return
				}
				errs <- err
				return
			}
			outputChars += len(plan)
			r.emitAutonomousProgress(events, prepared.Run.ID, limits, startedAt, iteration, outputChars, toolCalls, "")
			if stopped, err := r.stopIfCanceled(events, prepared.Run.ID); stopped || err != nil {
				if err != errRunCanceled {
					errs <- err
				}
				return
			}

			act, err := r.runAutonomousStep(ctx, events, prepared, iteration, "act", autonomousActPrompt(prepared.WorkerAgent), autonomousActInput(task, plan, state))
			if err != nil {
				if err == errRunCanceled {
					return
				}
				errs <- err
				return
			}
			outputChars += len(act)
			lastAct = act
			r.emitAutonomousProgress(events, prepared.Run.ID, limits, startedAt, iteration, outputChars, toolCalls, "")
			if stopped, err := r.stopIfCanceled(events, prepared.Run.ID); stopped || err != nil {
				if err != errRunCanceled {
					errs <- err
				}
				return
			}

			review, err := r.runAutonomousStep(ctx, events, prepared, iteration, "review", autonomousReviewPrompt(), autonomousReviewInput(task, plan, act))
			if err != nil {
				if err == errRunCanceled {
					return
				}
				errs <- err
				return
			}
			outputChars += len(review)
			lastReview = review
			r.emitAutonomousProgress(events, prepared.Run.ID, limits, startedAt, iteration, outputChars, toolCalls, "")
			if stopped, err := r.stopIfCanceled(events, prepared.Run.ID); stopped || err != nil {
				if err != errRunCanceled {
					errs <- err
				}
				return
			}

			decide, err := r.runAutonomousStep(ctx, events, prepared, iteration, "decide", autonomousDecidePrompt(), autonomousDecideInput(task, observe, plan, act, review, iteration, limits))
			if err != nil {
				if err == errRunCanceled {
					return
				}
				errs <- err
				return
			}
			outputChars += len(decide)
			r.emitAutonomousProgress(events, prepared.Run.ID, limits, startedAt, iteration, outputChars, toolCalls, "")

			decision := parseAutonomousDecision(decide)
			if !decision.ValidJSON || decision.Decision == "" {
				decision.Decision = "continue"
				decision.Reason = "decide step did not return usable JSON; continuing within safety limits"
				if iteration == limits.MaxIterations {
					decision.Decision = "stop"
					decision.Reason = "decide step did not return usable JSON at max iteration"
				}
				log.Printf("autonomous_decision_fallback run_id=%s iteration=%d reason=%q", prepared.Run.ID, iteration, decision.Reason)
			}
			limitReason := limitStopReason(limits, startedAt, outputChars, toolCalls, enforceLegacyResourceLimits)
			if limitReason != "" {
				decision.Decision = "stop"
				decision.Reason = limitReason
			}
			if limitReason == "" && decision.Decision != "ask_user" {
				if need := inferHumanInputNeed(observe, plan, act, review, decide); need.Needed {
					decision.Decision = "ask_user"
					decision.Reason = fmt.Sprintf("human input guard detected missing user information in %s: %s", need.Source, need.Evidence)
					decision.Question = need.Question
					log.Printf("autonomous_hitl_guard run_id=%s iteration=%d source=%s evidence=%q question=%q", prepared.Run.ID, iteration, need.Source, need.Evidence, need.Question)
				}
			}
			if decision.Decision == "stop" || iteration == limits.MaxIterations {
				r.emitAutonomousProgress(events, prepared.Run.ID, limits, startedAt, iteration, outputChars, toolCalls, decision.Reason)
			}
			log.Printf("autonomous_iteration_decision run_id=%s iteration=%d decision=%s reason=%q output_chars=%d", prepared.Run.ID, iteration, decision.Decision, decision.Reason, outputChars)

			if decision.Decision == "ask_user" {
				question := strings.TrimSpace(decision.Question)
				if question == "" {
					question = "Please provide the missing information needed to continue."
				}
				step, err := r.store.CreateCollaborationStep(domain.CollaborationStep{
					RunID:          prepared.Run.ID,
					ConversationID: prepared.Run.ConversationID,
					Role:           "human_input",
					AgentID:        prepared.WorkerAgent.ID,
					Status:         domain.CollaborationStepRunning,
					Iteration:      iteration,
					Input:          strings.TrimSpace(decision.Reason),
					Output:         question,
				})
				if err != nil {
					errs <- err
					return
				}
				run, err := r.store.UpdateRunStatus(prepared.Run.ID, domain.RunWaitingForUser, "")
				if err != nil {
					errs <- err
					return
				}
				log.Printf("autonomous_waiting_for_user run_id=%s iteration=%d question=%q reason=%q", prepared.Run.ID, iteration, question, decision.Reason)
				r.publishStage(ctx, step, domain.EventStageStarted)
				r.publishRunLifecycle(ctx, run, domain.EventRunWaitingForUser, map[string]any{"status": run.Status, "question": question})
				events <- liveStageEvent(step)
				events <- liveRunEvent(run)
				return
			}

			if decision.Decision == "stop" || iteration == limits.MaxIterations {
				reason := strings.TrimSpace(decision.Reason)
				if reason == "" && iteration == limits.MaxIterations {
					reason = "max_iterations reached"
				}
				finalAnswer := strings.TrimSpace(decision.FinalAnswer)
				if finalAnswer == "" {
					finalAnswer = formatAutonomousFallbackFinal(task, lastAct, lastReview, reason)
				}
				if err := r.emitAutonomousFinal(ctx, events, prepared, iteration, reason, finalAnswer); err != nil {
					errs <- err
				}
				return
			}

			state = fmt.Sprintf("Previous observation:\n%s\n\nPrevious plan:\n%s\n\nPrevious action result:\n%s\n\nPrevious review:\n%s\n\nDecision: %s", observe, plan, act, review, decision.Reason)
		}
	}()

	return events, errs
}

func (r *Runtime) runAutonomousStep(ctx context.Context, events chan<- domain.RunEvent, prepared PreparedCollaborationRun, iteration int, role string, systemPrompt string, input string) (string, error) {
	if err := r.heartbeatRun(prepared.Run.ID); err != nil {
		return "", err
	}
	if stopped, err := r.stopIfCanceled(events, prepared.Run.ID); stopped || err != nil {
		if err != nil {
			return "", err
		}
		return "", errRunCanceled
	}
	step, err := r.store.CreateCollaborationStep(domain.CollaborationStep{
		RunID:          prepared.Run.ID,
		ConversationID: prepared.Run.ConversationID,
		Role:           role,
		AgentID:        prepared.WorkerAgent.ID,
		Status:         domain.CollaborationStepRunning,
		Iteration:      iteration,
		Input:          input,
	})
	if err != nil {
		return "", err
	}
	events <- liveStageEvent(step)
	r.publishStage(ctx, step, domain.EventStageStarted)

	if stopped, err := r.stopIfCanceled(events, prepared.Run.ID); stopped || err != nil {
		if err != nil {
			return "", err
		}
		return "", errRunCanceled
	}
	retrievedMemories, retrievedChunks := r.retrieveContext(ctx, prepared.Run.ID, input, true, true, map[string]any{
		"executor":  ExecutorNative,
		"framework": "agentflow-native",
	})
	result, err := r.turnEngine.Execute(ctx, turnpkg.Request{
		RunID: prepared.Run.ID, StepID: step.ID, ConversationID: prepared.Run.ConversationID,
		Agent: prepared.WorkerAgent, Role: role, SystemPrompt: systemPrompt, Input: input,
		ModelMode: turnpkg.ModelModeText,
		Context:   turnpkg.Context{Memories: retrievedMemories, Chunks: retrievedChunks},
		Metadata:  map[string]any{"iteration": iteration},
		Sink:      r.runEventSink(),
	}, nil)
	if err != nil {
		if ctx.Err() != nil {
			if stopped, stopErr := r.stopIfCanceled(events, prepared.Run.ID); stopped || stopErr != nil {
				if stopErr != nil {
					return "", stopErr
				}
				return "", errRunCanceled
			}
		}
		failed, updateErr := r.store.UpdateCollaborationStep(step.ID, domain.CollaborationStepFailed, "", err.Error())
		if updateErr == nil {
			events <- liveStageEvent(failed)
			r.publishStage(ctx, failed, domain.EventStageFailed)
		}
		return "", err
	}
	if stopped, err := r.stopIfCanceled(events, prepared.Run.ID); stopped || err != nil {
		if err != nil {
			return "", err
		}
		return "", errRunCanceled
	}
	output := result.Output
	completed, err := r.store.UpdateCollaborationStep(step.ID, domain.CollaborationStepCompleted, output, "")
	if err != nil {
		return "", err
	}
	if err := r.heartbeatRun(prepared.Run.ID); err != nil {
		return "", err
	}
	events <- liveStageEvent(completed)
	r.publishStage(ctx, completed, domain.EventStageCompleted)
	return output, nil
}
