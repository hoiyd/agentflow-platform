package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
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
	agent, err := r.resolveAgent(agentID)
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
	log.Printf("autonomous_prepare run_id=%s agent_id=%s max_iterations=%d max_runtime=%s max_output_chars=%d max_tool_calls=%d", run.ID, agent.ID, r.autonomousLimits.MaxIterations, r.autonomousLimits.MaxRuntime, r.autonomousLimits.MaxOutputChars, r.autonomousLimits.MaxToolCalls)
	return PreparedCollaborationRun{WorkerAgent: agent, Run: run}, nil
}

func (r *Runtime) RunAutonomous(ctx context.Context, prepared PreparedCollaborationRun, task string) (<-chan CollaborationEvent, <-chan error) {
	return r.runAutonomousFromState(ctx, prepared, task, autonomousState{
		StartedAt: time.Now().UTC(),
		State:     "No prior autonomous work.",
		NextIter:  1,
	})
}

func (r *Runtime) ResumeAutonomous(ctx context.Context, runID string, userInput string) (<-chan CollaborationEvent, <-chan error) {
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

		agent, ok, err := r.store.GetAgent(run.AgentID)
		if err != nil {
			errs <- err
			return
		}
		if !ok {
			errs <- fmt.Errorf("agent not found")
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
		events <- CollaborationEvent{Type: "run", Run: run}
		events <- CollaborationEvent{Type: "collaboration_step", Step: completedCheckpoint}

		state := rebuildAutonomousState(steps, completedCheckpoint)
		prepared := PreparedCollaborationRun{WorkerAgent: agent, Run: run}
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

func (r *Runtime) ResumeRecoverableAutonomous(ctx context.Context, runID string, recoveryNote string) (<-chan CollaborationEvent, <-chan error) {
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
		agent, ok, err := r.store.GetAgent(run.AgentID)
		if err != nil {
			errs <- err
			return
		}
		if !ok {
			errs <- fmt.Errorf("agent not found")
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
		events <- CollaborationEvent{Type: "run", Run: run}
		events <- CollaborationEvent{Type: "collaboration_step", Step: recoveryStep}

		state := rebuildRecoverableAutonomousState(steps, recoveryStep)
		prepared := PreparedCollaborationRun{WorkerAgent: agent, Run: run}
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

func (r *Runtime) runAutonomousFromState(ctx context.Context, prepared PreparedCollaborationRun, task string, initial autonomousState) (<-chan CollaborationEvent, <-chan error) {
	events := make(chan CollaborationEvent)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

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
		if nextIter > r.autonomousLimits.MaxIterations {
			reason := "recovered after max_iterations reached"
			r.emitAutonomousProgress(events, startedAt, r.autonomousLimits.MaxIterations, outputChars, toolCalls, reason)
			if err := r.finishAutonomous(ctx, events, prepared, r.autonomousLimits.MaxIterations, task, lastAct, lastReview, reason); err != nil {
				errs <- err
			}
			return
		}

		for iteration := nextIter; iteration <= r.autonomousLimits.MaxIterations; iteration++ {
			if err := r.heartbeatRun(prepared.Run.ID); err != nil {
				errs <- err
				return
			}
			r.emitAutonomousProgress(events, startedAt, iteration, outputChars, toolCalls, "")
			if stopped, err := r.stopIfCanceled(events, prepared.Run.ID); stopped || err != nil {
				if err != errRunCanceled {
					errs <- err
				}
				return
			}
			if reason := r.limitStopReason(startedAt, outputChars, toolCalls); reason != "" {
				r.emitAutonomousProgress(events, startedAt, iteration, outputChars, toolCalls, reason)
				if err := r.finishAutonomous(ctx, events, prepared, iteration, task, lastAct, lastReview, reason); err != nil {
					errs <- err
				}
				return
			}

			log.Printf("autonomous_iteration_start run_id=%s iteration=%d output_chars=%d tool_calls=%d", prepared.Run.ID, iteration, outputChars, toolCalls)

			observe, err := r.runAutonomousStep(ctx, events, prepared, iteration, "observe", autonomousObservePrompt(), autonomousObserveInput(task, state, r.autonomousLimits, startedAt, outputChars, toolCalls))
			if err != nil {
				if err == errRunCanceled {
					return
				}
				errs <- err
				return
			}
			outputChars += len(observe)
			r.emitAutonomousProgress(events, startedAt, iteration, outputChars, toolCalls, "")
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
			r.emitAutonomousProgress(events, startedAt, iteration, outputChars, toolCalls, "")
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
			r.emitAutonomousProgress(events, startedAt, iteration, outputChars, toolCalls, "")
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
			r.emitAutonomousProgress(events, startedAt, iteration, outputChars, toolCalls, "")
			if stopped, err := r.stopIfCanceled(events, prepared.Run.ID); stopped || err != nil {
				if err != errRunCanceled {
					errs <- err
				}
				return
			}

			decide, err := r.runAutonomousStep(ctx, events, prepared, iteration, "decide", autonomousDecidePrompt(), autonomousDecideInput(task, observe, plan, act, review, iteration, r.autonomousLimits))
			if err != nil {
				if err == errRunCanceled {
					return
				}
				errs <- err
				return
			}
			outputChars += len(decide)
			r.emitAutonomousProgress(events, startedAt, iteration, outputChars, toolCalls, "")

			decision := parseAutonomousDecision(decide)
			if !decision.ValidJSON || decision.Decision == "" {
				decision.Decision = "continue"
				decision.Reason = "decide step did not return usable JSON; continuing within safety limits"
				if iteration == r.autonomousLimits.MaxIterations {
					decision.Decision = "stop"
					decision.Reason = "decide step did not return usable JSON at max iteration"
				}
				log.Printf("autonomous_decision_fallback run_id=%s iteration=%d reason=%q", prepared.Run.ID, iteration, decision.Reason)
			}
			limitReason := r.limitStopReason(startedAt, outputChars, toolCalls)
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
			if decision.Decision == "stop" || iteration == r.autonomousLimits.MaxIterations {
				r.emitAutonomousProgress(events, startedAt, iteration, outputChars, toolCalls, decision.Reason)
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
				events <- CollaborationEvent{Type: "collaboration_step", Step: step}
				events <- CollaborationEvent{Type: "run", Run: run}
				return
			}

			if decision.Decision == "stop" || iteration == r.autonomousLimits.MaxIterations {
				reason := strings.TrimSpace(decision.Reason)
				if reason == "" && iteration == r.autonomousLimits.MaxIterations {
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

func (r *Runtime) runAutonomousStep(ctx context.Context, events chan<- CollaborationEvent, prepared PreparedCollaborationRun, iteration int, role string, systemPrompt string, input string) (string, error) {
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
	events <- CollaborationEvent{Type: "collaboration_step", Step: step}

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
	tracePayload := map[string]any{
		"role":        role,
		"agent_id":    prepared.WorkerAgent.ID,
		"iteration":   iteration,
		"system":      systemPrompt,
		"input":       input,
		"input_chars": len(input),
	}
	for key, value := range retrievalTracePayload(retrievedMemories, retrievedChunks) {
		tracePayload[key] = value
	}
	contextualPrompt := promptWithRetrievedContext(systemPrompt, retrievedMemories, retrievedChunks)
	span := r.trace.LLMStart(ctx, prepared.Run.ID, step.ID, tracePayload)
	completion, err := r.openAI.CompleteTextDetailed(ctx, contextualPrompt, input)
	if err != nil {
		if ctx.Err() != nil {
			if stopped, stopErr := r.stopIfCanceled(events, prepared.Run.ID); stopped || stopErr != nil {
				if stopErr != nil {
					return "", stopErr
				}
				return "", errRunCanceled
			}
		}
		r.trace.Error(ctx, prepared.Run.ID, step.ID, map[string]any{
			"source":    "llm",
			"role":      role,
			"agent_id":  prepared.WorkerAgent.ID,
			"iteration": iteration,
			"error":     err.Error(),
		})
		failed, updateErr := r.store.UpdateCollaborationStep(step.ID, domain.CollaborationStepFailed, "", err.Error())
		if updateErr == nil {
			events <- CollaborationEvent{Type: "collaboration_step", Step: failed}
		}
		return "", err
	}
	if stopped, err := r.stopIfCanceled(events, prepared.Run.ID); stopped || err != nil {
		if err != nil {
			return "", err
		}
		return "", errRunCanceled
	}
	output := completion.Text
	r.trace.LLMEnd(ctx, span, map[string]any{
		"role":                  role,
		"agent_id":              prepared.WorkerAgent.ID,
		"iteration":             iteration,
		"model":                 completion.Model,
		"output":                output,
		"output_chars":          len(output),
		"prompt_tokens":         completion.Usage.PromptTokens,
		"completion_tokens":     completion.Usage.CompletionTokens,
		"total_tokens":          completion.Usage.TotalTokens,
		"token_usage_estimated": completion.Usage.Estimated,
	})
	completed, err := r.store.UpdateCollaborationStep(step.ID, domain.CollaborationStepCompleted, output, "")
	if err != nil {
		return "", err
	}
	if err := r.heartbeatRun(prepared.Run.ID); err != nil {
		return "", err
	}
	events <- CollaborationEvent{Type: "collaboration_step", Step: completed}
	return output, nil
}

func (r *Runtime) heartbeatRun(runID string) error {
	if strings.TrimSpace(runID) == "" {
		return nil
	}
	_, err := r.store.UpdateRunHeartbeat(runID)
	return err
}

func (r *Runtime) stopIfCanceled(events chan<- CollaborationEvent, runID string) (bool, error) {
	run, ok, err := r.store.GetRun(runID)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	if run.Status != domain.RunCanceling && run.Status != domain.RunCanceled {
		return false, nil
	}
	if run.Status == domain.RunCanceling {
		run, err = r.store.UpdateRunStatus(run.ID, domain.RunCanceled, "canceled by user")
		if err != nil {
			return false, err
		}
	}
	log.Printf("autonomous_canceled run_id=%s", run.ID)
	events <- CollaborationEvent{Type: "run", Run: run}
	return true, errRunCanceled
}

func (r *Runtime) limitStopReason(startedAt time.Time, outputChars int, toolCalls int) string {
	if time.Since(startedAt) >= r.autonomousLimits.MaxRuntime {
		return "max_runtime reached"
	}
	if outputChars >= r.autonomousLimits.MaxOutputChars {
		return "max_output_chars reached"
	}
	if toolCalls >= r.autonomousLimits.MaxToolCalls {
		return "max_tool_calls reached"
	}
	return ""
}

func (r *Runtime) finishAutonomous(ctx context.Context, events chan<- CollaborationEvent, prepared PreparedCollaborationRun, iteration int, task string, lastAct string, lastReview string, reason string) error {
	return r.emitAutonomousFinal(ctx, events, prepared, iteration, reason, formatAutonomousFallbackFinal(task, lastAct, lastReview, reason))
}

func (r *Runtime) emitAutonomousFinal(ctx context.Context, events chan<- CollaborationEvent, prepared PreparedCollaborationRun, iteration int, reason string, finalAnswer string) error {
	output := strings.TrimSpace(finalAnswer)
	if output == "" {
		output = "Autonomous run stopped without a final answer."
	}
	step, err := r.store.CreateCollaborationStep(domain.CollaborationStep{
		RunID:          prepared.Run.ID,
		ConversationID: prepared.Run.ConversationID,
		Role:           "final",
		AgentID:        prepared.WorkerAgent.ID,
		Status:         domain.CollaborationStepCompleted,
		Iteration:      iteration,
		Input:          "Stop reason: " + strings.TrimSpace(reason),
		Output:         output,
	})
	if err != nil {
		return err
	}
	log.Printf("autonomous_final run_id=%s iteration=%d reason=%q output_len=%d", prepared.Run.ID, iteration, reason, len(output))
	events <- CollaborationEvent{Type: "collaboration_step", Step: step}
	emitFinalDeltas(ctx, output, events)
	return nil
}

func parseAutonomousDecision(value string) autonomousDecision {
	jsonText, err := extractJSONObject(value)
	if err != nil {
		return autonomousDecision{}
	}
	var decoded autonomousDecision
	if err := json.Unmarshal([]byte(jsonText), &decoded); err != nil {
		return autonomousDecision{}
	}
	decoded.ValidJSON = true
	decoded.Decision = strings.ToLower(strings.TrimSpace(decoded.Decision))
	if decoded.Decision != "continue" && decoded.Decision != "stop" && decoded.Decision != "rework" && decoded.Decision != "ask_user" {
		decoded.Decision = ""
	}
	if decoded.Decision == "rework" {
		decoded.Decision = "continue"
	}
	decoded.Reason = strings.TrimSpace(decoded.Reason)
	decoded.Question = strings.TrimSpace(decoded.Question)
	decoded.FinalAnswer = strings.TrimSpace(decoded.FinalAnswer)
	return decoded
}

func inferHumanInputNeed(observe string, plan string, act string, review string, decide string) humanInputNeed {
	sections := []struct {
		source string
		text   string
	}{
		{source: "review", text: review},
		{source: "plan", text: plan},
		{source: "act", text: act},
		{source: "observe", text: observe},
		{source: "decide", text: decide},
	}
	for _, section := range sections {
		if evidence, ok := humanInputEvidence(section.text); ok {
			return humanInputNeed{
				Needed:   true,
				Source:   section.source,
				Evidence: evidence,
				Question: humanInputQuestion(evidence),
			}
		}
	}
	return humanInputNeed{}
}

func humanInputEvidence(value string) (string, bool) {
	for _, line := range strings.Split(value, "\n") {
		line = cleanHumanInputEvidence(line)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if containsAny(lower, humanInputNegativeMarkers()) {
			continue
		}
		if containsAny(lower, humanInputPositiveMarkers()) {
			return truncateAutonomousText(line, 220), true
		}
	}
	return "", false
}

func cleanHumanInputEvidence(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimLeft(value, "-*•0123456789.、) \t")
	return strings.TrimSpace(value)
}

func humanInputQuestion(evidence string) string {
	evidence = strings.TrimSpace(evidence)
	if evidence == "" {
		return "Please provide the missing information needed to continue."
	}
	if strings.Contains(evidence, "?") || strings.Contains(evidence, "？") {
		return evidence
	}
	if strings.Contains(evidence, "用户") || strings.Contains(evidence, "需要") || strings.Contains(evidence, "缺少") {
		return "请补充继续完成任务所需的信息：" + evidence
	}
	return "Please provide the missing information needed to continue: " + evidence
}

func containsAny(value string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func truncateAutonomousText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func humanInputPositiveMarkers() []string {
	return []string{
		"need user input",
		"needs user input",
		"require user input",
		"requires user input",
		"ask the user",
		"ask user",
		"user must provide",
		"user needs to provide",
		"need the user to provide",
		"requires clarification",
		"require clarification",
		"need clarification from the user",
		"missing information",
		"missing user information",
		"cannot proceed without",
		"can't proceed without",
		"cannot continue without",
		"can't continue without",
		"blocked until user",
		"waiting for user",
		"need more information from the user",
		"需要用户输入",
		"需要用户提供",
		"需要用户补充",
		"需要用户确认",
		"需要用户澄清",
		"需要进一步澄清",
		"需要补充信息",
		"缺少用户",
		"缺少必要信息",
		"无法继续",
		"不能继续",
		"等待用户",
		"请用户提供",
		"请用户补充",
		"请补充",
	}
}

func humanInputNegativeMarkers() []string {
	return []string{
		"no user input",
		"no additional user input",
		"does not require user input",
		"doesn't require user input",
		"user input is not required",
		"无需用户",
		"不需要用户",
		"无需补充",
		"不需要补充",
		"没有缺少",
	}
}

func (r *Runtime) emitAutonomousProgress(events chan<- CollaborationEvent, startedAt time.Time, iteration int, outputChars int, toolCalls int, stopReason string) {
	events <- CollaborationEvent{
		Type: "autonomous_progress",
		Progress: AutonomousProgress{
			Iteration:         iteration,
			MaxIterations:     r.autonomousLimits.MaxIterations,
			ElapsedSeconds:    int(time.Since(startedAt).Seconds()),
			MaxRuntimeSeconds: int(r.autonomousLimits.MaxRuntime.Seconds()),
			OutputChars:       outputChars,
			MaxOutputChars:    r.autonomousLimits.MaxOutputChars,
			ToolCalls:         toolCalls,
			MaxToolCalls:      r.autonomousLimits.MaxToolCalls,
			StopReason:        strings.TrimSpace(stopReason),
		},
	}
}

func autonomousObservePrompt() string {
	return "You are the Observe stage in a bounded autonomous agent loop. Summarize the task, known context, progress, constraints, risks, and missing information. Do not solve the whole task."
}

func autonomousPlanPrompt() string {
	return "You are the Plan stage in a bounded autonomous agent loop. Produce the next concrete action plan for one iteration. Keep it short, testable, and aware of limits."
}

func autonomousActPrompt(agent domain.Agent) string {
	return fmt.Sprintf("You are the Act stage in a bounded autonomous agent loop using this agent persona.\n\nAgent: %s\nDescription: %s\nPersona instructions: %s\n\nExecute the current plan as far as possible in this single iteration.", agent.Name, agent.Description, agent.SystemPrompt)
}

func autonomousReviewPrompt() string {
	return "You are the Review stage in a bounded autonomous agent loop. Check whether the action result satisfies the original user task, identify gaps, and state what still needs work."
}

func autonomousDecidePrompt() string {
	return `You are the Decide stage in a bounded autonomous agent loop. Return only valid JSON:
{"decision":"continue|stop|rework|ask_user","reason":"short reason","question":"question for the user when decision is ask_user, otherwise empty","final_answer":"final user-facing answer when stopping, otherwise empty"}
If observation, plan, action, or review says information is missing from the user, return ask_user and ask one concrete question. Use ask_user when the task cannot be completed responsibly without one concise piece of information from the user. Stop only when the task is complete or when another iteration would not materially improve the answer.`
}

func autonomousObserveInput(task string, state string, limits AutonomousLimits, startedAt time.Time, outputChars int, toolCalls int) string {
	return fmt.Sprintf("User task:\n%s\n\nCurrent state:\n%s\n\nSafety limits:\n- max_iterations: %d\n- max_runtime: %s\n- max_output_chars: %d\n- max_tool_calls: %d\n\nCurrent usage:\n- elapsed: %s\n- output_chars: %d\n- tool_calls: %d", task, state, limits.MaxIterations, limits.MaxRuntime, limits.MaxOutputChars, limits.MaxToolCalls, time.Since(startedAt).Round(time.Second), outputChars, toolCalls)
}

func autonomousPlanInput(task string, observe string, state string) string {
	return fmt.Sprintf("User task:\n%s\n\nObservation:\n%s\n\nPrior state:\n%s", task, observe, state)
}

func autonomousActInput(task string, plan string, state string) string {
	return fmt.Sprintf("User task:\n%s\n\nCurrent plan:\n%s\n\nPrior state:\n%s", task, plan, state)
}

func autonomousReviewInput(task string, plan string, act string) string {
	return fmt.Sprintf("User task:\n%s\n\nPlan:\n%s\n\nAction result:\n%s", task, plan, act)
}

func autonomousDecideInput(task string, observe string, plan string, act string, review string, iteration int, limits AutonomousLimits) string {
	return fmt.Sprintf("User task:\n%s\n\nIteration: %d / %d\n\nObservation:\n%s\n\nPlan:\n%s\n\nAction result:\n%s\n\nReview:\n%s\n\nReturn JSON only.", task, iteration, limits.MaxIterations, observe, plan, act, review)
}

func formatAutonomousFallbackFinal(task string, act string, review string, reason string) string {
	parts := []string{
		"Autonomous run result",
		"",
		"Task:",
		strings.TrimSpace(task),
	}
	if strings.TrimSpace(act) != "" {
		parts = append(parts, "", "Best current result:", strings.TrimSpace(act))
	}
	if strings.TrimSpace(review) != "" {
		parts = append(parts, "", "Review:", strings.TrimSpace(review))
	}
	if strings.TrimSpace(reason) != "" {
		parts = append(parts, "", "Stop reason:", strings.TrimSpace(reason))
	}
	return strings.Join(parts, "\n")
}

func latestHumanInputStep(steps []domain.CollaborationStep) *domain.CollaborationStep {
	var latest *domain.CollaborationStep
	for i := range steps {
		if steps[i].Role != "human_input" {
			continue
		}
		if latest == nil || steps[i].CreatedAt.After(latest.CreatedAt) {
			latest = &steps[i]
		}
	}
	return latest
}

func firstAutonomousTask(steps []domain.CollaborationStep) string {
	for _, step := range steps {
		if step.Role == "observe" && strings.TrimSpace(step.Input) != "" {
			if before, _, ok := strings.Cut(step.Input, "\n\nCurrent state:"); ok {
				return strings.TrimSpace(strings.TrimPrefix(before, "User task:"))
			}
			return strings.TrimSpace(step.Input)
		}
	}
	return "Continue the autonomous task."
}

func rebuildAutonomousState(previousSteps []domain.CollaborationStep, completedHumanInput domain.CollaborationStep) autonomousState {
	maxIteration := completedHumanInput.Iteration
	outputChars := len(completedHumanInput.Output)
	lastAct := ""
	lastReview := ""
	parts := []string{"Human input received:\n" + strings.TrimSpace(completedHumanInput.Output)}
	for _, step := range previousSteps {
		outputChars += len(step.Output)
		if step.Iteration > maxIteration {
			maxIteration = step.Iteration
		}
		if step.Role == "act" && strings.TrimSpace(step.Output) != "" {
			lastAct = step.Output
		}
		if step.Role == "review" && strings.TrimSpace(step.Output) != "" {
			lastReview = step.Output
		}
		if step.Iteration == completedHumanInput.Iteration && strings.TrimSpace(step.Output) != "" {
			parts = append(parts, fmt.Sprintf("%s output:\n%s", step.Role, step.Output))
		}
	}
	return autonomousState{
		StartedAt:   time.Now().UTC(),
		OutputChars: outputChars,
		State:       strings.Join(parts, "\n\n"),
		LastAct:     lastAct,
		LastReview:  lastReview,
		NextIter:    maxIteration + 1,
	}
}

func rebuildRecoverableAutonomousState(previousSteps []domain.CollaborationStep, recoveryStep domain.CollaborationStep) autonomousState {
	maxIteration := 0
	outputChars := len(recoveryStep.Output)
	lastAct := ""
	lastReview := ""
	parts := []string{"Recoverable run resumed from saved steps."}
	if note := strings.TrimSpace(recoveryStep.Output); note != "" {
		parts = append(parts, "Recovery note:\n"+note)
	}
	for _, step := range previousSteps {
		if step.Status != domain.CollaborationStepCompleted {
			continue
		}
		if step.Role == "recovery" {
			continue
		}
		outputChars += len(step.Output)
		if step.Iteration > maxIteration {
			maxIteration = step.Iteration
		}
		if step.Role == "act" && strings.TrimSpace(step.Output) != "" {
			lastAct = step.Output
		}
		if step.Role == "review" && strings.TrimSpace(step.Output) != "" {
			lastReview = step.Output
		}
		if strings.TrimSpace(step.Output) != "" {
			parts = append(parts, fmt.Sprintf("Previous %s output:\n%s", step.Role, step.Output))
		}
	}
	return autonomousState{
		StartedAt:   time.Now().UTC(),
		OutputChars: outputChars,
		State:       strings.Join(parts, "\n\n"),
		LastAct:     lastAct,
		LastReview:  lastReview,
		NextIter:    maxIteration + 1,
	}
}

func nextAutonomousIteration(steps []domain.CollaborationStep) int {
	next := 1
	for _, step := range steps {
		if step.Iteration >= next {
			next = step.Iteration + 1
		}
	}
	return next
}
