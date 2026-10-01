package projection

import (
	"encoding/json"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestMessageReasoningUsesExplicitMessageIdentityAndRunOrder(t *testing.T) {
	messages := []domain.Message{{ID: "old", ConversationID: "c", Role: "assistant"}, {ID: "new", ConversationID: "c", Role: "assistant"}, {ID: "user", ConversationID: "c", Role: "user"}}
	reason := func(sequence int64, call, text string) domain.RunEvent {
		return domain.RunEvent{Type: domain.EventModelReasoning, RunID: "r", ConversationID: "c", TurnID: "t", Sequence: sequence,
			Payload: map[string]any{"model_call_id": call, "format": "deepseek_reasoning_content", "status": "complete", "text": text}}
	}
	bind := func(sequence int64, id string) domain.RunEvent {
		return domain.RunEvent{Type: domain.EventCitationResolved, RunID: "r", ConversationID: "c", Sequence: sequence, Payload: map[string]any{"message_id": id}}
	}
	bad := reason(8, "bad", "foreign conversation")
	bad.ConversationID = "other"
	user := reason(9, "user-call", "not a user message")
	malformed := reason(11, "broken", "malformed")
	malformed.Payload["text"] = 123
	events := []domain.RunEvent{bind(6, "new"), reason(4, "call2", "second"), bind(3, "old"), reason(1, "call1", "first"), reason(2, "call1", "duplicate"), bind(5, "new"), bad, user, bind(10, "user"), malformed}
	result := AttachMessageReasoning(messages, events)
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if strings.Contains(text, "duplicate") || strings.Contains(text, "foreign conversation") || strings.Contains(text, "not a user message") || strings.Contains(text, "malformed") {
		t.Fatal(text)
	}
	var rows []struct {
		Reasoning []struct{ Text, RunID string } `json:"reasoning"`
	}
	if err := json.Unmarshal(encoded, &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows[0].Reasoning) != 1 || rows[0].Reasoning[0].Text != "first" || len(rows[1].Reasoning) != 1 || rows[1].Reasoning[0].Text != "second" || len(rows[2].Reasoning) != 0 {
		t.Fatal(text)
	}
	original, _ := json.Marshal(messages)
	if strings.Contains(string(original), "reasoning") {
		t.Fatal("projection mutated source messages")
	}
}
