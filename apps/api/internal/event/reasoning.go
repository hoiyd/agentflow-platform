package event

import "agentflow-platform/apps/api/internal/domain"

// ModelReasoningPayload is a sanitized display copy, never continuation state.
// Text is a bounded replacement, not an answer delta; only complete calls release it.
type ModelReasoningPayload struct {
	ModelCallID string `json:"model_call_id"`
	Format      string `json:"format"`
	Status      string `json:"status"`
	Text        string `json:"text,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`
}

func (ModelReasoningPayload) supports(eventType domain.RunEventType) bool {
	return eventType == domain.EventModelReasoning
}
