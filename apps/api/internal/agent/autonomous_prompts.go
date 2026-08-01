package agent

import (
	"fmt"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

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
