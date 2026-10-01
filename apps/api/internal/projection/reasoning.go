package projection

import (
	"cmp"
	"encoding/json"
	"slices"

	"agentflow-platform/apps/api/internal/domain"
)

// AttachMessageReasoning reads sanitized durable output without changing model
// history. Run completion already emits citation.resolved with the assistant
// message ID, including uncited answers; use that explicit binding, never time
// proximity or the latest message. Consumed entries cannot leak into a later Resume.
func AttachMessageReasoning(messages []domain.Message, events []domain.RunEvent) []domain.Message {
	result := slices.Clone(messages)
	byID := map[string]int{}
	for i, message := range result {
		if message.Role == "assistant" {
			byID[message.ID] = i
		}
	}
	ordered := slices.Clone(events)
	slices.SortStableFunc(ordered, func(a, b domain.RunEvent) int {
		if order := cmp.Compare(a.RunID, b.RunID); order != 0 {
			return order
		}
		return cmp.Compare(a.Sequence, b.Sequence)
	})
	pending := map[string][]domain.MessageReasoning{}
	seen := map[string]bool{}
	for _, item := range ordered {
		if item.Type == domain.EventModelReasoning {
			encoded, err := json.Marshal(item.Payload)
			var entry domain.MessageReasoning
			if err != nil || json.Unmarshal(encoded, &entry) != nil || entry.Status != "complete" || entry.Text == "" || len(entry.Text) > 16384 || entry.ModelCallID == "" || entry.Format == "" || item.RunID == "" || item.TurnID == "" {
				continue
			}
			entry.RunID, entry.TurnID, entry.StageID = item.RunID, item.TurnID, item.StageID
			key := item.RunID + "\x00" + item.TurnID + "\x00" + entry.ModelCallID
			if seen[key] {
				continue
			}
			// Keep the Conversation identity in the pending key for scope isolation.
			pending[item.RunID+"\x00"+item.ConversationID] = append(pending[item.RunID+"\x00"+item.ConversationID], entry)
			seen[key] = true
		} else if item.Type == domain.EventCitationResolved {
			i, ok := byID[stringPayload(item.Payload, "message_id")]
			if !ok || result[i].ConversationID != item.ConversationID {
				continue
			}
			key := item.RunID + "\x00" + item.ConversationID
			if len(pending[key]) == 0 {
				continue
			}
			result[i].Reasoning = append(slices.Clone(result[i].Reasoning), pending[key]...)
			delete(pending, key)
		}
	}
	return result
}
