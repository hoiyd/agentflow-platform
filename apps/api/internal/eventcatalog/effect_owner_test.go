package eventcatalog

import (
	"agentflow-platform/apps/api/internal/domain"
	"testing"
)

func TestReconciliationRequiresRealStageOrTurn(t *testing.T) {
	for _, kind := range []domain.RunEventType{domain.EventToolEffectReconciliationStarted, domain.EventToolEffectReconciled, domain.EventToolEffectReconciliationFailed} {
		for _, owner := range []struct {
			stage, turn string
			valid       bool
		}{{"", "turn", true}, {"stage", "", true}, {"stage", "turn", true}, {"", "", false}, {" ", " ", false}} {
			item := domain.RunEvent{Type: kind, SchemaVersion: domain.CurrentRunEventSchemaVersion, RunID: "run", StageID: owner.stage, TurnID: owner.turn}
			if err := ValidateEnvelope(item); (err == nil) != owner.valid {
				t.Fatalf("%s owner=%+v err=%v", kind, owner, err)
			}
		}
	}
}
