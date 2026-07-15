package recovery

import (
	"log"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

const staleRunMessage = "run heartbeat expired; previous worker may have crashed"

type Store interface {
	ListStaleRunningRuns(time.Time) ([]domain.Run, error)
	UpdateRunStatus(string, domain.RunStatus, string) (domain.Run, error)
}

func MarkStaleRunningRuns(appStore Store, staleTimeout time.Duration) (int, error) {
	if staleTimeout <= 0 {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-staleTimeout)
	runs, err := appStore.ListStaleRunningRuns(cutoff)
	if err != nil {
		return 0, err
	}
	for _, run := range runs {
		if _, err := appStore.UpdateRunStatus(run.ID, domain.RunFailedRecoverable, staleRunMessage); err != nil {
			return 0, err
		}
		log.Printf("native_recovery_marked run_id=%s heartbeat_at=%v cutoff=%s", run.ID, run.HeartbeatAt, cutoff.Format(time.RFC3339))
	}
	return len(runs), nil
}
