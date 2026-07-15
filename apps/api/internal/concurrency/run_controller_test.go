package concurrency

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRunControllerRejectsWhenCapacityIsFull(t *testing.T) {
	controller := NewRunController(RunOptions{MaxConcurrent: 1, QueueSize: 1, WaitTimeout: time.Second})
	first, err := controller.Reserve()
	if err != nil {
		t.Fatalf("reserve first: %v", err)
	}
	defer first.Cancel()
	second, err := controller.Reserve()
	if err != nil {
		t.Fatalf("reserve queued: %v", err)
	}
	defer second.Cancel()
	if _, err := controller.Reserve(); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("expected queue full, got %v", err)
	}
}

func TestRunControllerSerializesSameConversation(t *testing.T) {
	controller := NewRunController(RunOptions{MaxConcurrent: 2, QueueSize: 1, WaitTimeout: time.Second})
	first, _ := controller.Reserve()
	releaseFirst, err := first.Start(context.Background(), "conversation-1")
	if err != nil {
		t.Fatalf("start first: %v", err)
	}

	second, _ := controller.Reserve()
	started := make(chan func(), 1)
	go func() {
		release, startErr := second.Start(context.Background(), "conversation-1")
		if startErr == nil {
			started <- release
		}
	}()

	select {
	case release := <-started:
		release()
		t.Fatal("same conversation started concurrently")
	case <-time.After(30 * time.Millisecond):
	}

	releaseFirst()
	select {
	case release := <-started:
		release()
	case <-time.After(time.Second):
		t.Fatal("second run did not start after conversation release")
	}
}

func TestRunControllerAllowsDifferentConversations(t *testing.T) {
	controller := NewRunController(RunOptions{MaxConcurrent: 2, QueueSize: 0, WaitTimeout: time.Second})
	first, _ := controller.Reserve()
	releaseFirst, err := first.Start(context.Background(), "conversation-1")
	if err != nil {
		t.Fatalf("start first: %v", err)
	}
	defer releaseFirst()

	second, _ := controller.Reserve()
	releaseSecond, err := second.Start(context.Background(), "conversation-2")
	if err != nil {
		t.Fatalf("different conversation should run concurrently: %v", err)
	}
	releaseSecond()
}

func TestRunControllerTimesOutQueuedRun(t *testing.T) {
	controller := NewRunController(RunOptions{MaxConcurrent: 1, QueueSize: 1, WaitTimeout: 20 * time.Millisecond})
	first, _ := controller.Reserve()
	releaseFirst, err := first.Start(context.Background(), "conversation-1")
	if err != nil {
		t.Fatalf("start first: %v", err)
	}
	defer releaseFirst()

	second, _ := controller.Reserve()
	if _, err := second.Start(context.Background(), "conversation-2"); !errors.Is(err, ErrQueueTimeout) {
		t.Fatalf("expected queue timeout, got %v", err)
	}
}
