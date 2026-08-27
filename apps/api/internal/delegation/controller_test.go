package delegation

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestControllerEnforcesBoundsWithoutQueue(t *testing.T) {
	controller := NewController(Options{MaxConcurrent: 2, MaxPerParent: 1, MaxDepth: 1})
	first, err := controller.Reserve("parent-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	if _, err := controller.Reserve("parent-1", 1); !errors.Is(err, ErrParentCapacity) {
		t.Fatalf("expected parent capacity error, got %v", err)
	}
	second, err := controller.Reserve("parent-2", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release()
	if _, err := controller.Reserve("parent-3", 1); !errors.Is(err, ErrCapacity) {
		t.Fatalf("expected global capacity error, got %v", err)
	}
	if _, err := controller.Reserve("parent-3", 2); !errors.Is(err, ErrDepth) {
		t.Fatalf("expected depth error, got %v", err)
	}
}

func TestControllerPropagatesParentCancellation(t *testing.T) {
	controller := NewController(Options{MaxConcurrent: 1, MaxPerParent: 1, MaxDepth: 1})
	reservation, err := controller.Reserve("parent", 1)
	if err != nil {
		t.Fatal(err)
	}
	defer reservation.Release()
	ctx, cancel := context.WithCancelCause(context.Background())
	reservation.Bind("child", cancel)
	cause := errors.New("parent canceled")
	controller.CancelParent("parent", cause)
	<-ctx.Done()
	if !errors.Is(context.Cause(ctx), cause) {
		t.Fatalf("cause = %v", context.Cause(ctx))
	}
}

func TestReservationReleaseKeepsSiblingCancellationRegistered(t *testing.T) {
	controller := NewController(Options{MaxConcurrent: 2, MaxPerParent: 2, MaxDepth: 1})
	first, err := controller.Reserve("parent", 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := controller.Reserve("parent", 1)
	if err != nil {
		t.Fatal(err)
	}
	firstCtx, firstCancel := context.WithCancelCause(context.Background())
	secondCtx, secondCancel := context.WithCancelCause(context.Background())
	first.Bind("child-1", firstCancel)
	second.Bind("child-2", secondCancel)
	first.Release()
	defer second.Release()
	cause := errors.New("parent canceled")
	controller.CancelParent("parent", cause)
	select {
	case <-secondCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("remaining child was not canceled")
	}
	if firstCtx.Err() != nil {
		t.Fatal("released child was canceled through stale registration")
	}
}
