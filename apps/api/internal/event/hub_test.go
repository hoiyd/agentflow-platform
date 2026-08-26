package event

import (
	"context"
	"errors"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func TestSnapshotAndSubscribeDoesNotLoseCommittedEventAtHandoff(t *testing.T) {
	hub := NewHub(4)
	loaderStarted := make(chan struct{})
	releaseLoader := make(chan struct{})
	result := make(chan Subscription, 1)
	errs := make(chan error, 1)
	go func() {
		subscription, err := hub.SnapshotAndSubscribe(context.Background(), "run-1", func() (domain.RunProjectionSnapshot, error) {
			close(loaderStarted)
			<-releaseLoader
			return domain.RunProjectionSnapshot{AsOfSequence: 1}, nil
		})
		result <- subscription
		errs <- err
	}()
	<-loaderStarted
	published := make(chan struct{})
	go func() {
		hub.PublishCommitted(domain.RunEvent{ID: "event-3", RunID: "run-1", Type: domain.EventRunCompleted, Sequence: 3})
		hub.PublishCommitted(domain.RunEvent{ID: "event-1", RunID: "run-1", Type: domain.EventRunStarted, Sequence: 1})
		hub.PublishCommitted(domain.RunEvent{ID: "event-2", RunID: "run-1", Type: domain.EventRunStarted, Sequence: 2})
		close(published)
	}()
	close(releaseLoader)
	subscription := <-result
	defer subscription.Close()
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	for _, want := range []int64{2, 3} {
		select {
		case item := <-subscription.Events:
			if item.Sequence != want {
				t.Fatalf("sequence = %d, want %d", item.Sequence, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("committed event %d was lost at snapshot/subscription handoff", want)
		}
	}
	<-published

	hub.PublishLive(domain.RunEvent{RunID: "run-1", Type: domain.EventModelDelta, SchemaVersion: domain.CurrentRunEventSchemaVersion, Payload: map[string]any{"delta": "a"}})
	select {
	case item := <-subscription.Events:
		if item.Type != domain.EventModelDelta || item.Sequence != 0 {
			t.Fatalf("unexpected live event: %#v", item)
		}
	case <-time.After(time.Second):
		t.Fatal("live event was not delivered")
	}
}

func TestSlowSubscriberIsDisconnectedWithoutBlockingPublisher(t *testing.T) {
	hub := NewHub(1)
	subscription, err := hub.SnapshotAndSubscribe(context.Background(), "run-1", func() (domain.RunProjectionSnapshot, error) {
		return domain.RunProjectionSnapshot{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	hub.PublishCommitted(domain.RunEvent{RunID: "run-1", Type: domain.EventRunStarted, Sequence: 1})
	hub.PublishCommitted(domain.RunEvent{RunID: "run-1", Type: domain.EventRunCompleted, Sequence: 2})
	err = <-subscription.Errors
	var typed *SubscriptionError
	if !errors.Is(err, ErrSubscriberLagged) || !errors.As(err, &typed) || typed.Code != "event_subscriber_lagged" || typed.RunID != "run-1" {
		t.Fatalf("error = %#v", err)
	}
}
