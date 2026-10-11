// Package processfixture coordinates real subprocess fault tests without
// coupling the injected failure to host startup or scheduling speed.
package processfixture

import (
	"context"
	"sync"
)

type deadline struct {
	context.Context
	done chan struct{}
}

func (d *deadline) Done() <-chan struct{} { return d.done }
func (d *deadline) Err() error {
	select {
	case <-d.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

// Deadline expires only when the test confirms the intended fault boundary.
// It is not a clock/timer test; callers must bound handshake waits separately.
func Deadline() (context.Context, func()) {
	d := &deadline{Context: context.Background(), done: make(chan struct{})}
	var once sync.Once
	return d, func() { once.Do(func() { close(d.done) }) }
}
