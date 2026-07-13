package httpapi

import (
	"net/http"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

func (h *Handler) getEpisodeReport(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(strings.TrimPrefix(r.URL.Path, "/api/runs/"))
	id = strings.TrimSpace(strings.TrimSuffix(id, "/episode"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "run id is required")
		return
	}

	replay, ok, err := h.store.GetRunReplay(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	agent, ok, err := h.store.GetAgent(replay.Run.AgentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}

	writeJSON(w, http.StatusOK, buildEpisodeReport(replay, domain.NormalizeAgentConfig(agent)))
}

func buildEpisodeReport(replay domain.RunReplay, agent domain.Agent) domain.EpisodeReport {
	report := domain.EpisodeReport{
		Run:          replay.Run,
		Conversation: replay.Conversation,
		Agent:        agent,
		Task:         episodeTask(replay),
		FinalOutput:  episodeFinalOutput(replay),
		Messages:     replay.Messages,
		Steps:        replay.Steps,
		TraceSummary: replay.Summary,
		Retrievals:   episodeRetrievals(replay.RunEvents),
		LLMCalls:     episodeLLMCalls(replay.RunEvents),
		ToolCalls:    episodeToolCalls(replay.RunEvents),
		Errors:       episodeErrors(replay),
	}
	report.Verification = episodeVerification(report)
	return report
}

func episodeTask(replay domain.RunReplay) string {
	for _, message := range replay.Messages {
		if message.Role == "user" && strings.TrimSpace(message.Content) != "" {
			return strings.TrimSpace(message.Content)
		}
	}
	for _, step := range replay.Steps {
		if strings.TrimSpace(step.Input) != "" {
			return strings.TrimSpace(step.Input)
		}
	}
	return ""
}

func episodeFinalOutput(replay domain.RunReplay) string {
	for i := len(replay.Steps) - 1; i >= 0; i-- {
		step := replay.Steps[i]
		if strings.EqualFold(step.Role, "final") && strings.TrimSpace(step.Output) != "" {
			return strings.TrimSpace(step.Output)
		}
	}
	for i := len(replay.Messages) - 1; i >= 0; i-- {
		message := replay.Messages[i]
		if message.Role == "assistant" && strings.TrimSpace(message.Content) != "" {
			return strings.TrimSpace(message.Content)
		}
	}
	for i := len(replay.Steps) - 1; i >= 0; i-- {
		if strings.TrimSpace(replay.Steps[i].Output) != "" {
			return strings.TrimSpace(replay.Steps[i].Output)
		}
	}
	return ""
}

func episodeRetrievals(events []domain.RunEvent) domain.EpisodeRetrievals {
	retrievals := domain.EpisodeRetrievals{
		Memories: []map[string]any{},
		Chunks:   []map[string]any{},
	}
	for _, event := range events {
		memories := mapSlicePayload(event.Payload["retrieved_memories"])
		chunks := mapSlicePayload(event.Payload["retrieved_chunks"])
		if event.Type == domain.EventRetrievalCompleted || len(memories) > 0 || len(chunks) > 0 {
			retrievals.EventCount++
		}
		retrievals.Memories = append(retrievals.Memories, memories...)
		retrievals.Chunks = append(retrievals.Chunks, chunks...)
	}
	return retrievals
}

func episodeLLMCalls(events []domain.RunEvent) []domain.EpisodeLLMCall {
	calls := []domain.EpisodeLLMCall{}
	for _, event := range events {
		if event.Type != domain.EventModelCompleted {
			continue
		}
		calls = append(calls, domain.EpisodeLLMCall{
			EventID:             event.ID,
			StepID:              event.StageID,
			Role:                stringPayload(event.Payload, "role"),
			AgentID:             stringPayload(event.Payload, "agent_id"),
			Model:               stringPayload(event.Payload, "model"),
			Framework:           stringPayload(event.Payload, "framework"),
			PromptTokens:        intPayload(event.Payload, "prompt_tokens"),
			CompletionTokens:    intPayload(event.Payload, "completion_tokens"),
			TotalTokens:         intPayload(event.Payload, "total_tokens"),
			TokenUsageEstimated: boolPayload(event.Payload, "token_usage_estimated"),
			OutputChars:         intPayload(event.Payload, "output_chars"),
			DurationMS:          int64(intPayload(event.Payload, "duration_ms")),
		})
	}
	return calls
}

func episodeToolCalls(events []domain.RunEvent) []domain.EpisodeToolCall {
	calls := []domain.EpisodeToolCall{}
	for _, event := range events {
		if event.Type != domain.EventToolCompleted && event.Type != domain.EventToolFailed {
			continue
		}
		calls = append(calls, domain.EpisodeToolCall{
			EventID:    event.ID,
			StepID:     event.StageID,
			ToolName:   stringPayload(event.Payload, "tool_name"),
			ToolCallID: stringPayload(event.Payload, "tool_call_id"),
			Error:      stringPayload(event.Payload, "error"),
			DurationMS: int64(intPayload(event.Payload, "duration_ms")),
		})
	}
	return calls
}

func episodeErrors(replay domain.RunReplay) []domain.EpisodeError {
	errors := []domain.EpisodeError{}
	if strings.TrimSpace(replay.Run.Error) != "" {
		errors = append(errors, domain.EpisodeError{
			Source:  "run",
			Message: strings.TrimSpace(replay.Run.Error),
		})
	}
	for _, step := range replay.Steps {
		if strings.TrimSpace(step.Error) == "" {
			continue
		}
		errors = append(errors, domain.EpisodeError{
			Source:  "step:" + step.Role,
			StepID:  step.ID,
			Message: strings.TrimSpace(step.Error),
		})
	}
	for _, event := range replay.RunEvents {
		if event.Type != domain.EventModelFailed && event.Type != domain.EventToolFailed && event.Type != domain.EventRetrievalFailed && event.Type != domain.EventMemorySyncFailed {
			continue
		}
		message := stringPayload(event.Payload, "error")
		if message == "" {
			message = "trace error"
		}
		source := stringPayload(event.Payload, "source")
		if source == "" {
			source = "trace"
		}
		errors = append(errors, domain.EpisodeError{
			Source:  source,
			EventID: event.ID,
			StepID:  event.StageID,
			Message: message,
		})
	}
	return errors
}

func episodeVerification(report domain.EpisodeReport) domain.EpisodeVerification {
	verification := domain.EpisodeVerification{
		Status:   "needs_review",
		Evidence: []string{},
		Warnings: []string{},
	}
	if report.Run.Status == domain.RunCompleted {
		verification.Evidence = append(verification.Evidence, "Run completed")
	} else {
		verification.Warnings = append(verification.Warnings, "Run status is "+string(report.Run.Status))
	}
	if len(report.Errors) == 0 && report.TraceSummary.ErrorCount == 0 {
		verification.Evidence = append(verification.Evidence, "No errors recorded")
	} else {
		verification.Warnings = append(verification.Warnings, "Errors recorded")
	}
	if strings.TrimSpace(report.FinalOutput) != "" {
		verification.Evidence = append(verification.Evidence, "Final output captured")
	} else {
		verification.Warnings = append(verification.Warnings, "No final output captured")
	}
	if len(report.Retrievals.Memories) > 0 || len(report.Retrievals.Chunks) > 0 {
		verification.Evidence = append(verification.Evidence, "Retrieved context captured")
	} else {
		verification.Warnings = append(verification.Warnings, "No retrieved context captured")
	}

	if report.Run.Status == domain.RunFailedRecoverable {
		verification.Status = "needs_review"
		return verification
	}
	if report.Run.Status == domain.RunFailed || len(report.Errors) > 0 || report.TraceSummary.ErrorCount > 0 {
		verification.Status = "failed"
		return verification
	}
	if report.Run.Status == domain.RunCompleted && strings.TrimSpace(report.FinalOutput) != "" {
		verification.Status = "passed"
	}
	return verification
}

func mapSlicePayload(value any) []map[string]any {
	items, ok := value.([]any)
	if !ok {
		return []map[string]any{}
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if mapped, ok := item.(map[string]any); ok {
			result = append(result, mapped)
		}
	}
	return result
}

func stringPayload(payload map[string]any, key string) string {
	value, ok := payload[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func intPayload(payload map[string]any, key string) int {
	switch value := payload[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func boolPayload(payload map[string]any, key string) bool {
	value, ok := payload[key].(bool)
	return ok && value
}
