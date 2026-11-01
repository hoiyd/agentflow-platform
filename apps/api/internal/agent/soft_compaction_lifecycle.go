package agent

import "context"

// One best-effort soft job per Runtime; excess triggers are skipped, not queued
// behind already completed Runs. Hard/overflow compaction keeps its Run context.
func (r *Runtime) startSoftCompaction(work func(context.Context)) bool {
	if r == nil {
		return false
	}
	r.compactionMu.Lock()
	defer r.compactionMu.Unlock()
	if r.compactionClosed {
		return false
	}
	if r.compactionDone != nil {
		select {
		case <-r.compactionDone:
		default:
			return false
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	r.compactionCancel, r.compactionDone = cancel, done
	go func() { defer close(done); defer cancel(); work(ctx) }()
	return true
}

// CloseCompaction cancels auxiliary work and joins its durable settlement. A
// deadline is an incomplete drain: callers must not close its Store underneath it.
func (r *Runtime) CloseCompaction(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.compactionMu.Lock()
	r.compactionClosed = true
	if r.compactionCancel != nil {
		r.compactionCancel()
	}
	done := r.compactionDone
	r.compactionMu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	default:
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
