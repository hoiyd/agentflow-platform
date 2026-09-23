package event

import (
	"context"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestCanceledModelRequestStillRecordsTerminalFailure(t *testing.T) {
	store := &artifactEventStoreStub{}
	ctx, cancel := context.WithCancel(WithScope(context.Background(), Scope{
		RunID: "run-1", StageID: "stage-1", TurnID: "turn-1",
	}))
	cancel()
	NewRecorder(store).Error(ctx, "run-1", "stage-1", map[string]any{"error": "request canceled"})
	if store.calls != 1 || store.item.Type != domain.EventModelFailed || store.item.RunID != "run-1" || store.item.TurnID != "turn-1" {
		t.Fatalf("canceled model terminal event was lost: calls=%d item=%#v", store.calls, store.item)
	}
}
