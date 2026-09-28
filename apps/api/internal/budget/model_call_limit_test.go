package budget

import (
	"context"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestHasModelCallLimitUsesEffectiveReservationLimits(t *testing.T) {
	for _, test := range []struct {
		limits domain.RuntimeRunBudget
		want   bool
	}{
		{domain.RuntimeRunBudget{}, false},
		{domain.RuntimeRunBudget{MaxModelCalls: 2}, true},
		{domain.RuntimeRunBudget{MaxPromptTokens: 100}, true},
		{domain.RuntimeRunBudget{MaxTotalTokens: 100}, true},
		{domain.RuntimeRunBudget{MaxToolCalls: 2, MaxCompletionTokens: 10, MaxEstimatedCostMicros: 10, MaxRuntimeMS: 100}, false},
	} {
		tracker := NewTracker(nil, nil, trackerTestRun(test.limits))
		if got := HasModelCallLimit(WithController(context.Background(), tracker)); got != test.want {
			t.Fatalf("limits=%#v got=%v want=%v", test.limits, got, test.want)
		}
	}
	if HasModelCallLimit(context.Background()) || HasModelCallLimit(WithController(context.Background(), &controllerStub{})) {
		t.Fatal("unknown controller is not a proven bound")
	}
}
