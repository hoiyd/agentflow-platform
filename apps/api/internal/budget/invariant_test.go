package budget

import (
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestRuntimeInvariantDetectsDuplicateSettlement(t *testing.T) {
	entries := []domain.RunUsageEntry{
		{ID: "reservation", OperationID: "call-1", Kind: domain.UsageModelReservation},
		{ID: "settlement-1", OperationID: "call-1", Kind: domain.UsageModelSettlement},
		{ID: "settlement-2", OperationID: "call-1", Kind: domain.UsageModelSettlement},
	}
	failures := CheckRuntimeInvariants("run-1", entries)
	if len(failures) != 1 || failures[0].Code != "usage_duplicate_settlement" || failures[0].Owner != "budget" {
		t.Fatalf("unexpected failures: %#v", failures)
	}
}
