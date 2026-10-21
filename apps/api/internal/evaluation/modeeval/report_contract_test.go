package modeeval

import (
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

// Failure inventory: a failed/recoverable/canceled arm or unsettled reservation
// must stay in the denominator and must never publish a cost or gain delta.
func TestIncompleteEvidenceDisablesDeltas(t *testing.T) {
	for _, status := range []string{"failed", "failed_recoverable", "canceled", "not_evaluated", "completed"} {
		t.Run(status, func(t *testing.T) {
			base := Sample{Trial: 1, Mode: "single", Status: "completed", BoundaryHash: "same", Verified: true}
			arm := Sample{Trial: 1, Mode: "multi_agent", Status: status, BoundaryHash: "same"}
			if status == "completed" {
				arm.Usage = domain.RunUsageTotals{OpenReservations: 1}
			}
			report := Report{Samples: []Sample{base, arm}}
			summarize(&report)
			pair := report.Comparisons[0]
			if report.Gate.Passed || report.Gate.BlockingFailures == 0 || pair.Comparable || pair.VerifiedDelta != nil || pair.TokenDelta != nil || report.Summary[arm.Mode].Samples != 1 {
				t.Fatalf("incomplete evidence accepted: %+v %+v", pair, report.Gate)
			}
		})
	}
}
