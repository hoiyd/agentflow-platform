package projection

import (
	"encoding/json"
	"sort"

	"agentflow-platform/apps/api/internal/domain"
)

func buildPartialOutputs(run domain.Run, events []domain.RunEvent) []domain.PartialOutput {
	items := map[string]domain.PartialOutput{}
	var resumedAt int64
	for _, event := range events {
		if event.Type == domain.EventRunResumed {
			resumedAt = max(resumedAt, event.Sequence)
		}
		if event.Type != domain.EventModelOutputCheckpoint {
			continue
		}
		var item domain.PartialOutput
		data, err := json.Marshal(event.Payload)
		if err != nil || json.Unmarshal(data, &item) != nil {
			continue
		}
		item.RunID, item.StageID, item.TurnID, item.Sequence = event.RunID, event.StageID, event.TurnID, event.Sequence
		key := item.TurnID + ":" + item.Channel
		if item.Channel == "reasoning" {
			key += ":" + item.ModelCallID
		}
		if previous, ok := items[key]; !ok || previous.Sequence < item.Sequence {
			items[key] = item
		}
	}
	result := []domain.PartialOutput{}
	if run.Status == domain.RunCompleted {
		return result
	}
	for _, item := range items {
		if item.Status == "retracted" || item.Text == "" {
			continue
		}
		if run.Status == domain.RunFailed || run.Status == domain.RunFailedRecoverable || run.Status == domain.RunCanceled ||
			(item.Status == "provisional" && item.Sequence < resumedAt) {
			item.Status = "interrupted"
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Sequence < result[j].Sequence })
	if len(result) > domain.MaxPartialOutputEntries {
		result = result[len(result)-domain.MaxPartialOutputEntries:]
	}
	return result
}
