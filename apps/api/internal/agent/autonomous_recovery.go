package agent

import (
	"fmt"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

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
