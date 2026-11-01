package app

import (
	"context"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/recovery"
	"agentflow-platform/apps/api/internal/store"
	"agentflow-platform/apps/api/internal/testsupport/pgfixture"
)

// A failed deferred DB read must propagate through the actual application
// shutdown, without claiming drain success or closing persistence underneath it.
func TestColdStartFailurePreventsSuccessfulApplicationDrain(t *testing.T) {
	s, err := store.NewPostgresStore(pgfixture.DatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, err := s.CreateConversation("cold-start close failure")
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateRunWithContract("agent_planner", c.ID, domain.RuntimeSnapshot{SchemaVersion: domain.CurrentRuntimeSnapshotVersion, RunBudget: &domain.RuntimeRunBudget{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateRunStatus(run.ID, domain.RunRunning, ""); err != nil {
		t.Fatal(err)
	}
	probe := &coldStartStoreProbe{PostgresStore: s, reread: make(chan struct{})}
	scan, err := recovery.PrepareColdStart(probe, time.Nanosecond)
	if err != nil {
		t.Fatal(err)
	}
	// Force a genuine persistence failure, then wait for the read itself rather
	// than guessing how long startup or the timer will take on a CI host.
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	scan.Start()
	<-probe.reread
	a := &Application{store: probe, coldStart: scan}
	if err = a.Close(context.Background()); err == nil {
		t.Fatal("failed recovery reported as successful application drain")
	}
	if probe.closed {
		t.Fatal("application closed persistence after an unsuccessful recovery drain")
	}
}

type coldStartStoreProbe struct {
	*store.PostgresStore
	reads  int
	reread chan struct{}
	closed bool
}

func (s *coldStartStoreProbe) ListStaleRunningRuns(cutoff time.Time) ([]domain.Run, error) {
	runs, err := s.PostgresStore.ListStaleRunningRuns(cutoff)
	s.reads++
	if s.reads == 2 {
		close(s.reread)
	}
	return runs, err
}

func (s *coldStartStoreProbe) Close() error {
	s.closed = true
	return s.PostgresStore.Close()
}
