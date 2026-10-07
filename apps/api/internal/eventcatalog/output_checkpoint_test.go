package eventcatalog

import (
	"agentflow-platform/apps/api/internal/domain"
	"testing"
)

func TestOutputCheckpointRejectsInvalidDisplayContracts(t *testing.T) {
	valid := func() domain.RunEvent {
		return domain.RunEvent{Type: domain.EventModelOutputCheckpoint,
			RunID: "r", TurnID: "t", SchemaVersion: domain.CurrentRunEventSchemaVersion,
			Payload: map[string]any{"run_id": "r", "turn_id": "t", "channel": "answer", "revision": 1, "round": 1,
				"offset": 2, "text": "ok", "status": "provisional"}}
	}
	if err := ValidateDurableFact(valid()); err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct {
		key   string
		value any
	}{
		{"channel", "continuation"}, {"revision", 0}, {"round", 0}, {"offset", 1},
		{"status", "completed"}, {"run_id", "another"}, {"stage_id", "invented"}, {"attempt", -1},
		{"revision", "not-a-number"}, {"extra", make(chan int)},
	} {
		t.Run(change.key, func(t *testing.T) {
			item := valid()
			item.Payload[change.key] = change.value
			if err := ValidateDurableFact(item); err == nil {
				t.Fatalf("accepted invalid checkpoint: %#v", item)
			}
		})
	}
}
