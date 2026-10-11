package event

import "agentflow-platform/apps/api/internal/domain"

type ToolDiscoveryPayload struct {
	domain.ToolDiscoveryState
}

func (ToolDiscoveryPayload) supports(t domain.RunEventType) bool {
	return t == domain.EventToolDiscoveryUpdated
}
