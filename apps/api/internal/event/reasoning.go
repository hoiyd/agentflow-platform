package event

import "agentflow-platform/apps/api/internal/domain"

// ModelReasoningPayload is a sanitized display copy, never continuation state.
// Text is a bounded replacement, not an answer delta. Live prefixes are ephemeral;
// only the completed, sanitized copy is persisted as display content.
type ModelReasoningPayload struct {
	ModelCallID string `json:"model_call_id"`
	Format      string `json:"format"`
	Status      string `json:"status"`
	Text        string `json:"text,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`
}

func (ModelReasoningPayload) supports(eventType domain.RunEventType) bool {
	return eventType == domain.EventModelReasoning || eventType == domain.EventModelReasoningDelta
}

// EventType keeps live batches out of durable history without changing the
// shared Turn callback or duplicating this decision in each execution mode.
func (p ModelReasoningPayload) EventType() domain.RunEventType {
	if p.Status == "receiving" && p.Text != "" {
		return domain.EventModelReasoningDelta
	}
	return domain.EventModelReasoning
}
