package recovery

import (
	"context"
	"log"
	"sync"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

// ColdStart owns one expiry recheck of the pre-admission execution set. It is
// not a worker lease or a periodic scan of executions accepted by this process.
type ColdStart struct {
	store  Store
	cutoff time.Time
	due    time.Time
	ids    map[string]bool
	ctx    context.Context
	cancel context.CancelFunc
	start  sync.Once
	done   chan struct{}
	err    error
}

func PrepareColdStart(s Store, timeout time.Duration) (*ColdStart, error) {
	if timeout <= 0 {
		return nil, nil
	}
	now := time.Now().UTC()
	runs, err := s.ListStaleRunningRuns(now)
	if err != nil {
		return nil, err
	}
	if _, err = repairRuns(s, runs, now.Add(-timeout)); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &ColdStart{store: s, cutoff: now, due: now.Add(timeout), ids: map[string]bool{}, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	for _, run := range runs {
		r.ids[run.ID] = true
	}
	return r, nil
}

func (r *ColdStart) Start() {
	if r == nil {
		return
	}
	r.start.Do(func() {
		go func() {
			defer close(r.done)
			if len(r.ids) == 0 {
				return
			}
			timer := time.NewTimer(max(time.Until(r.due), 0))
			defer timer.Stop()
			select {
			case <-r.ctx.Done():
				return
			case <-timer.C:
			}
			if r.ctx.Err() != nil {
				return
			}
			// Cap the stale boundary at startup: even a late timer cannot repair a Run
			// renewed or resumed after this process took its snapshot.
			err := r.recheck()
			r.err = err
			if err != nil {
				log.Printf("cold_start_recheck_failed: %v", err)
			}
		}()
	})
}

func (r *ColdStart) recheck() error {
	// One bounded re-read handles a concurrent Stop/event append without making
	// a stale plan authoritative or turning recovery into a polling worker.
	for attempt := 0; attempt < 2; attempt++ {
		runs, err := r.store.ListStaleRunningRuns(r.cutoff)
		if err != nil {
			return err
		}
		pending := make([]domain.Run, 0, len(runs))
		for _, run := range runs {
			if r.ids[run.ID] {
				pending = append(pending, run)
			}
		}
		repaired, err := repairRuns(r.store, pending, r.cutoff)
		if err != nil || repaired == len(pending) {
			return err
		}
	}
	return nil
}

func (r *ColdStart) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.cancel()
	r.Start()
	select {
	case <-r.done:
		return r.err
	case <-ctx.Done():
		return ctx.Err()
	}
}
