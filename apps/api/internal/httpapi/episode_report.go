package httpapi

import (
	"fmt"
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
	scoped := h.scopedStore(r)
	if _, ok, err := scoped.GetRun(id); err != nil {
		writeFailure(w, http.StatusInternalServerError, err)
		return
	} else if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}

	replay, ok, err := scoped.GetRunReplay(id)
	if err != nil {
		writeFailure(w, http.StatusInternalServerError, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	agent, ok, err := h.store.GetAgent(replay.Run.AgentID)
	if err != nil {
		writeFailure(w, http.StatusInternalServerError, err)
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
	report.Verification = episodeVerification(replay)
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
		if event.Type != domain.EventModelFailed && event.Type != domain.EventToolFailed && event.Type != domain.EventRetrievalFailed && event.Type != domain.EventHistorySearchFailed && event.Type != domain.EventCompactionFailed && event.Type != domain.EventMemoryCandidateFailed && event.Type != domain.EventMemorySyncFailed && event.Type != domain.EventBudgetExceeded {
			continue
		}
		message := stringPayload(event.Payload, "error")
		if event.Type == domain.EventBudgetExceeded {
			message = fmt.Sprintf("run budget exceeded: resource=%s limit=%d used=%d requested=%d",
				stringPayload(event.Payload, "resource"), intPayload(event.Payload, "limit"),
				intPayload(event.Payload, "used"), intPayload(event.Payload, "requested"))
		}
		if message == "" {
			message = "trace error"
		}
		source := stringPayload(event.Payload, "error_source")
		if source == "" {
			source = stringPayload(event.Payload, "source")
		}
		if source == "" {
			source = "trace"
			if event.Type == domain.EventBudgetExceeded {
				source = "budget"
			}
		}
		errors = append(errors, domain.EpisodeError{
			Source: source, EventID: event.ID, StepID: event.StageID,
			Kind:      stringPayload(event.Payload, "error_kind"),
			Category:  stringPayload(event.Payload, "error_category"),
			Retryable: optionalBoolPayload(event.Payload, "retryable"), Message: message,
		})
	}
	return errors
}

func episodeVerification(replay domain.RunReplay) domain.EpisodeVerification {
	status := replay.Run.VerificationStatus
	if status == "" {
		status = domain.VerificationNotRequired
	}
	result := domain.EpisodeVerification{
		Status: status, Contract: replay.Run.CompletionContract,
		Evidence: []string{}, Warnings: []string{},
		Records: replay.VerificationEvidence, Artifacts: replay.VerificationArtifacts,
	}
	if replay.Run.CompletionContract == nil {
		result.Evidence = append(result.Evidence, "Completion contract not required")
		return result
	}
	for _, item := range replay.VerificationEvidence {
		if item.Status != domain.VerificationStale {
			result.SubjectHash = item.SubjectHash
		}
		message := fmt.Sprintf("%s %s: %s", item.VerifierID, item.Status, item.Summary)
		switch item.Status {
		case domain.VerificationPassed:
			result.Evidence = append(result.Evidence, message)
		case domain.VerificationFailed, domain.VerificationBlocked, domain.VerificationStale:
			result.Warnings = append(result.Warnings, message)
		}
	}
	if len(replay.VerificationEvidence) == 0 {
		result.Warnings = append(result.Warnings, "No verification evidence recorded")
	}
	return result
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

func optionalBoolPayload(payload map[string]any, key string) *bool {
	value, ok := payload[key].(bool)
	if !ok {
		return nil
	}
	return &value
}
