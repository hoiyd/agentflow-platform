package agent

import (
	"context"
	"errors"
	"testing"
)

// Failure inventory: duplicate triggers must not queue; shutdown cancels and
// joins cooperative work, reports uncooperative work and rejects new work.
func TestSoftCompactionOwnedLifecycle(t *testing.T) {
	r := &Runtime{}
	entered, finished := make(chan struct{}), make(chan struct{})
	if !r.startSoftCompaction(func(ctx context.Context) { close(entered); <-ctx.Done(); close(finished) }) {
		t.Fatal("not accepted")
	}
	<-entered
	if r.startSoftCompaction(func(context.Context) { t.Error("duplicate executed") }) {
		t.Fatal("duplicate accepted")
	}
	if err := r.CloseCompaction(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-finished
	if r.startSoftCompaction(func(context.Context) { t.Error("work after close") }) {
		t.Fatal("closed scheduler accepted work")
	}
}

func TestSoftCompactionDrainTimeoutIsNotSuccess(t *testing.T) {
	r := &Runtime{}
	entered, release := make(chan struct{}), make(chan struct{})
	r.startSoftCompaction(func(context.Context) { close(entered); <-release })
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.CloseCompaction(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost drain error: %v", err)
	}
	close(release)
	if err := r.CloseCompaction(context.Background()); err != nil {
		t.Fatal(err)
	}
}
