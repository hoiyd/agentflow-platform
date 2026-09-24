package openai

import (
	"context"
	"strings"
	"time"
)

func fallbackCompletion(systemPrompt string, prompt string) string {
	lower := strings.ToLower(systemPrompt)
	switch {
	case strings.Contains(lower, "planner"):
		return "Plan:\n1. Clarify the user's goal and expected output.\n2. Execute the main work using the selected worker agent.\n3. Review the result for completeness, risks, and missing details.\n\nSuccess criteria:\n- The final answer directly addresses the task.\n- Key assumptions and gaps are visible."
	case strings.Contains(lower, "worker"):
		return "Worker result:\nI executed the plan using the provided task context. The result is a concise draft that addresses the requested goal and preserves any important constraints.\n\nTask context:\n" + truncateText(prompt, 600)
	case strings.Contains(lower, "reviewer"):
		return "Review:\n- The result follows the requested fixed collaboration flow.\n- Remaining risk: verify domain-specific details when the task depends on external facts."
	case strings.Contains(lower, "finalizer"):
		return "Final answer:\nThe task was processed through Planner, Worker, Reviewer, and Finalizer stages. The final response combines the plan, execution result, and review notes into one answer.\n\n" + truncateText(prompt, 800)
	default:
		return "Generated response:\n" + truncateText(prompt, 800)
	}
}

func truncateText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func fallbackEventResponse(latest string) string {
	return "Local fallback response: backend streaming is working. Configure a credentialed Chat route to enable model-directed tool calling. You said: " + latest
}

func (c *Client) streamText(ctx context.Context, text string, delay time.Duration, events chan<- StreamEvent) {
	words := strings.Split(text, " ")
	for i, word := range words {
		select {
		case <-ctx.Done():
			return
		case events <- StreamEvent{Type: "delta", Delta: word + suffix(i, len(words))}:
			time.Sleep(delay)
		}
	}
}
