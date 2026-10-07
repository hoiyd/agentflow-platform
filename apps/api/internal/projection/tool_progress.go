package projection

import (
	"encoding/json"
	"sort"

	"agentflow-platform/apps/api/internal/domain"
)

// Only calls that actually emitted progress appear. Keep one latest replacement
// per call, bound the read model, and derive completion from lifecycle facts.
func buildToolProgress(run domain.Run, events []domain.RunEvent) []domain.ToolProgress {
	items := map[string]domain.ToolProgress{}
	var resumedAt int64
	for _, event := range events {
		if event.Type == domain.EventRunResumed {
			resumedAt = event.Sequence
		}
		call := stringPayload(event.Payload, "tool_call_id")
		key := event.TurnID + ":" + call
		switch event.Type {
		case domain.EventToolProgress:
			var update domain.ToolProgressUpdate
			data, err := json.Marshal(event.Payload)
			if err != nil || json.Unmarshal(data, &update) != nil || update.Validate() != nil || call == "" {
				continue
			}
			if _, exists := items[key]; !exists && len(items) == 32 {
				var oldest string
				for id, item := range items {
					if oldest == "" || item.Sequence < items[oldest].Sequence {
						oldest = id
					}
				}
				delete(items, oldest)
			}
			items[key] = domain.ToolProgress{ToolProgressUpdate: update,
				RunID: event.RunID, StageID: event.StageID, TurnID: event.TurnID, ToolCallID: call,
				ToolName: stringPayload(event.Payload, "tool_name"), Status: "running", Sequence: event.Sequence}
		case domain.EventToolCompleted, domain.EventToolFailed:
			if item, ok := items[key]; ok {
				item.Status = "completed"
				if event.Type == domain.EventToolFailed {
					item.Status = "failed"
					code := stringPayload(event.Payload, "error_code")
					if code == "execution_canceled" || code == "execution_timeout" || boolPayload(event.Payload, "synthetic") {
						item.Status = "interrupted"
					}
				}
				item.Sequence = event.Sequence
				items[key] = item
			}
		}
	}
	result := make([]domain.ToolProgress, 0, len(items))
	for _, item := range items {
		if item.Status == "running" && (item.Sequence < resumedAt || (run.Status != domain.RunRunning && run.Status != domain.RunQueued && run.Status != domain.RunCanceling)) {
			item.Status = "interrupted"
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Sequence < result[j].Sequence })
	return result
}
