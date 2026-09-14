package recovery

import (
	"fmt"
	"log"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
)

const staleRunMessage = "run heartbeat expired; previous worker may have crashed"

type Store interface {
	ListStaleRunningRuns(time.Time) ([]domain.Run, error)
	ListRunEvents(string) ([]domain.RunEvent, error)
	RepairInterruptedRun(domain.InterruptedRunRepair) (domain.InterruptedRunRepairResult, error)
}

// MarkStaleRunningRuns repairs durable lifecycle scopes before exposing a stale
// Run as recoverable. The store re-checks staleness and commits both changes
// atomically, making repeated startup scans idempotent.
func MarkStaleRunningRuns(appStore Store, staleTimeout time.Duration) (int, error) {
	if staleTimeout <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-staleTimeout)
	runs, err := appStore.ListStaleRunningRuns(cutoff)
	if err != nil {
		return 0, err
	}
	repaired := 0
	for _, run := range runs {
		events, err := appStore.ListRunEvents(run.ID)
		if err != nil {
			return repaired, err
		}
		terminal, err := event.PlanInterruptedLifecycleRepair(events, domain.InterruptedWorkerReason)
		if err != nil {
			return repaired, fmt.Errorf("plan interrupted run repair %s: %w", run.ID, err)
		}
		var cursor int64
		if len(events) > 0 {
			cursor = events[len(events)-1].Sequence
		}
		result, err := appStore.RepairInterruptedRun(domain.InterruptedRunRepair{
			RunID: run.ID, StaleBefore: cutoff, ExpectedEventCursor: cursor,
			TerminalEvents: terminal, ErrorMessage: staleRunMessage,
		})
		if err != nil {
			return repaired, err
		}
		if !result.Applied {
			continue
		}
		combined := append(append([]domain.RunEvent(nil), events...), result.AppendedEvents...)
		if err := event.ValidateLifecycle(combined); err != nil {
			return repaired, fmt.Errorf("validate repaired run %s: %w", run.ID, err)
		}
		repaired++
		log.Printf("native_recovery_repaired run_id=%s synthetic_events=%d heartbeat_at=%v cutoff=%s", run.ID, len(result.AppendedEvents), run.HeartbeatAt, cutoff.Format(time.RFC3339))
	}
	return repaired, nil
}
