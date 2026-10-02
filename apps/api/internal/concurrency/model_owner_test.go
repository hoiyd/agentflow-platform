package concurrency

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/inference/requestcontrol"
)

// Failure inventory: missing identity fails closed; same owner cannot multiply
// capacity with another Workspace; excess pending work fails immediately; owner
// waiters cannot hold global permits; cancellation/timeout/close and downstream
// rejection reclaim references; duplicate releases never steal another permit.
func TestOwnerAdmissionIsolationAndOverflow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := NewModelRequestLimiter(ModelRequestLimits{MaxConcurrent: 2, OwnerMaxConcurrent: 1, OwnerQueueSize: 1, OwnerWaitTimeout: time.Minute})
		a := requestcontrol.WithOwner(context.Background(), "owner-a")
		b := requestcontrol.WithOwner(context.Background(), "owner-b")
		first, err := l.AcquireRequest(a, "shared-key", 1)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(a)
		done := make(chan error, 1)
		go func() {
			release, err := l.AcquireRequest(ctx, "shared-key", 1)
			if release != nil {
				release()
			}
			done <- err
		}()
		synctest.Wait()
		if _, err := l.AcquireRequest(a, "different-key", 1); failure.Describe(err).Code != "owner_model_queue_full" {
			t.Fatalf("overflow: %v", err)
		}
		second, err := l.AcquireRequest(b, "shared-key", 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(l.global) != 2 {
			t.Fatal("owner waiter consumed global capacity")
		}
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		first()
		first()
		second()
		if len(l.owners) != 0 || len(l.global) != 0 {
			t.Fatal("capacity/reference leak")
		}
	})
}

func TestOwnerAdmissionWaitCleanup(t *testing.T) {
	for _, cause := range []string{"timeout", "close", "release", "parent deadline"} {
		t.Run(cause, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				l := NewModelRequestLimiter(ModelRequestLimits{MaxConcurrent: 2, OwnerMaxConcurrent: 1, OwnerQueueSize: 1, OwnerWaitTimeout: time.Second})
				ctx := requestcontrol.WithOwner(context.Background(), "owner")
				first, err := l.AcquireRequest(ctx, "", 1)
				if err != nil {
					t.Fatal(err)
				}
				if cause == "parent deadline" {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, time.Millisecond)
					defer cancel()
				}
				timing := &requestcontrol.AttemptTiming{}
				ctx = requestcontrol.WithAttemptTiming(ctx, timing)
				done := make(chan error, 1)
				go func() {
					release, err := l.AcquireRequest(ctx, "", 1)
					if release != nil {
						release()
					}
					done <- err
				}()
				synctest.Wait()
				switch cause {
				case "timeout":
					time.Sleep(2 * time.Second)
				case "parent deadline":
					time.Sleep(time.Second)
				case "close":
					time.Sleep(time.Millisecond)
					l.Close()
					l.Close()
				case "release":
					time.Sleep(time.Millisecond)
					first()
				}
				err = <-done
				want := map[string]string{"timeout": "owner_model_queue_timeout", "close": "model_limiter_closed", "release": "", "parent deadline": "timeout"}[cause]
				if failure.Describe(err).Code != want {
					t.Fatalf("want %q: %v", want, err)
				}
				if timing.OwnerWait <= 0 {
					t.Fatal("owner wait missing")
				}
				first()
				if len(l.owners) != 0 || len(l.global) != 0 {
					t.Fatal("capacity/reference leak")
				}
			})
		})
	}
}

func TestOwnerAdmissionRejections(t *testing.T) {
	l := NewModelRequestLimiter(ModelRequestLimits{MaxConcurrent: 2, OwnerMaxConcurrent: 1, TokensPerPeriod: 1})
	if _, err := l.AcquireRequest(context.Background(), "", 1); failure.Describe(err).Code != "model_owner_required" {
		t.Fatalf("missing owner: %v", err)
	}
	ctx := requestcontrol.WithOwner(context.Background(), "owner")
	if _, err := l.AcquireRequest(ctx, "key", 2); failure.Describe(err).Code != "request_token_capacity_exceeded" {
		t.Fatal(err)
	}
	if len(l.owners) != 0 {
		t.Fatal("rate rejection leaked owner")
	}
	l.Close()
	if _, err := l.AcquireRequest(ctx, "", 1); failure.Describe(err).Code != "model_limiter_closed" {
		t.Fatal(err)
	}
}

func TestOwnerAdmissionDownstreamWaitCleanup(t *testing.T) {
	for _, phase := range []string{"rate", "global"} {
		t.Run(phase, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				limits := ModelRequestLimits{MaxConcurrent: 1, OwnerMaxConcurrent: 1, OwnerQueueSize: 1}
				if phase == "rate" {
					limits.RequestsPerPeriod = 1
				}
				l := NewModelRequestLimiter(limits)
				first, err := l.AcquireRequest(requestcontrol.WithOwner(context.Background(), "a"), "key", 1)
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(requestcontrol.WithOwner(context.Background(), "b"))
				done := make(chan error, 1)
				go func() { _, err := l.AcquireRequest(ctx, "key", 1); done <- err }()
				synctest.Wait()
				cancel()
				if err := <-done; !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				first()
				if len(l.owners) != 0 || len(l.global) != 0 {
					t.Fatal("downstream wait leaked owner")
				}
			})
		})
	}
}

func TestOwnerAccessRecheckedBeforeTransmission(t *testing.T) {
	calls := 0
	l := NewModelRequestLimiter(ModelRequestLimits{MaxConcurrent: 2, OwnerMaxConcurrent: 1, OwnerResolver: func(context.Context) (string, error) {
		calls++
		if calls == 2 {
			return "", &requestcontrol.OwnerAdmissionError{Code: "model_owner_unavailable"}
		}
		return "owner", nil
	}})
	if _, err := l.AcquireRequest(context.Background(), "key", 1); failure.Describe(err).Code != "model_owner_unavailable" {
		t.Fatal(err)
	}
	if len(l.owners) != 0 || len(l.global) != 0 {
		t.Fatal("revoked access leaked capacity")
	}
}

func TestOwnerCloseWakesDownstreamWaiters(t *testing.T) {
	for _, phase := range []string{"rate", "global"} {
		t.Run(phase, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				limits := ModelRequestLimits{MaxConcurrent: 1, OwnerMaxConcurrent: 1}
				if phase == "rate" {
					limits.RequestsPerPeriod = 1
				}
				l := NewModelRequestLimiter(limits)
				first, err := l.AcquireRequest(requestcontrol.WithOwner(context.Background(), "first"), "key", 1)
				if err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() {
					_, err := l.AcquireRequest(requestcontrol.WithOwner(context.Background(), "second"), "key", 1)
					done <- err
				}()
				synctest.Wait()
				l.Close()
				if err := <-done; failure.Describe(err).Code != "model_limiter_closed" {
					t.Fatal(err)
				}
				first()
				if len(l.owners) != 0 || len(l.global) != 0 {
					t.Fatal("close leaked references")
				}
			})
		})
	}
}
