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
	FinalAnswer string `json:"final_answer"`
}

func (r *Runtime) PrepareAutonomousRun(ctx context.Context, conversationID string) (PreparedCollaborationRun, error) {
	agent, err := r.resolveAgent("")
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
	events := make(chan CollaborationEvent)
	errs := make(chan error, 1)

	go func() {
		defer close(events)
		defer close(errs)

		startedAt := time.Now().UTC()
		outputChars := 0
		toolCalls := 0
		state := "No prior autonomous work."
		lastAct := ""
		lastReview := ""

		for iteration := 1; iteration <= r.autonomousLimits.MaxIterations; iteration++ {
			if stopped, err := r.stopIfCanceled(events, prepared.Run.ID); stopped || err != nil {
				if err != errRunCanceled {
					errs <- err
				}
				return
			}
			if reason := r.limitStopReason(startedAt, outputChars, toolCalls); reason != "" {
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

			decision := parseAutonomousDecision(decide)
			if decision.Decision == "" {
				decision.Decision = "continue"
				decision.Reason = "decision JSON was unavailable; continuing within safety limits"
				if iteration == r.autonomousLimits.MaxIterations {
					decision.Decision = "stop"
					decision.Reason = "decision JSON was unavailable at max iteration"
				}
			}
			if reason := r.limitStopReason(startedAt, outputChars, toolCalls); reason != "" {
				decision.Decision = "stop"
				decision.Reason = reason
			}
			log.Printf("autonomous_iteration_decision run_id=%s iteration=%d decision=%s reason=%q output_chars=%d", prepared.Run.ID, iteration, decision.Decision, decision.Reason, outputChars)

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
	output, err := r.openAI.CompleteText(ctx, systemPrompt, input)
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
	completed, err := r.store.UpdateCollaborationStep(step.ID, domain.CollaborationStepCompleted, output, "")
	if err != nil {
		return "", err
	}
	events <- CollaborationEvent{Type: "collaboration_step", Step: completed}
	return output, nil
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
	decoded.Decision = strings.ToLower(strings.TrimSpace(decoded.Decision))
	if decoded.Decision != "continue" && decoded.Decision != "stop" && decoded.Decision != "rework" {
		decoded.Decision = ""
	}
	if decoded.Decision == "rework" {
		decoded.Decision = "continue"
	}
	decoded.Reason = strings.TrimSpace(decoded.Reason)
	decoded.FinalAnswer = strings.TrimSpace(decoded.FinalAnswer)
	return decoded
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
{"decision":"continue|stop|rework","reason":"short reason","final_answer":"final user-facing answer when stopping, otherwise empty"}
Stop when the task is complete or when another iteration would not materially improve the answer.`
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
