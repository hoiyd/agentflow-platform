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
	if typed.Error() != "event_subscriber_lagged: "+ErrSubscriberLagged.Error() {
		t.Fatalf("unexpected typed error text: %q", typed.Error())
	}
}

func TestHubValidatesSubscriptionAndLoaderFailures(t *testing.T) {
	var nilHub *Hub
	if _, err := nilHub.SnapshotAndSubscribe(context.Background(), "run-1", func() (domain.RunProjectionSnapshot, error) {
		return domain.RunProjectionSnapshot{}, nil
	}); err == nil {
		t.Fatal("nil hub should be rejected")
	}

	hub := NewHub(0)
	if hub.bufferSize != 128 {
		t.Fatalf("default buffer size=%d want 128", hub.bufferSize)
	}
	if _, err := hub.SnapshotAndSubscribe(context.Background(), "run-1", nil); err == nil {
		t.Fatal("nil loader should be rejected")
	}
	wantErr := errors.New("snapshot unavailable")
	if _, err := hub.SnapshotAndSubscribe(context.Background(), "run-1", func() (domain.RunProjectionSnapshot, error) {
		return domain.RunProjectionSnapshot{}, wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("loader error=%v want %v", err, wantErr)
	}
}

func TestSubscriptionCancellationClosesChannelsOnce(t *testing.T) {
	hub := NewHub(2)
	ctx, cancel := context.WithCancel(context.Background())
	subscription, err := hub.SnapshotAndSubscribe(ctx, "run-1", func() (domain.RunProjectionSnapshot, error) {
		return domain.RunProjectionSnapshot{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case _, ok := <-subscription.Events:
		if ok {
			t.Fatal("event channel should be closed after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("context cancellation did not close subscription")
	}
	subscription.Close()
	subscription.Close()
	Subscription{}.Close()
}

func TestHubIgnoresInvalidPublicationsAndOldDurableEvents(t *testing.T) {
	hub := NewHub(2)
	subscription, err := hub.SnapshotAndSubscribe(context.Background(), "run-1", func() (domain.RunProjectionSnapshot, error) {
		return domain.RunProjectionSnapshot{AsOfSequence: 2}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Close()

	var nilHub *Hub
	nilHub.PublishCommitted(domain.RunEvent{})
	nilHub.PublishLive(domain.RunEvent{})
	hub.PublishCommitted(domain.RunEvent{RunID: "", Type: domain.EventRunStarted, Sequence: 3})
	hub.PublishCommitted(domain.RunEvent{RunID: "run-1", Type: domain.EventRunStarted, Sequence: 0})
	hub.PublishCommitted(domain.RunEvent{RunID: "run-1", Type: domain.EventModelDelta, Sequence: 3})
	hub.PublishCommitted(domain.RunEvent{RunID: "run-1", Type: domain.EventRunStarted, Sequence: 2})
	hub.PublishLive(domain.RunEvent{RunID: "run-1", Type: domain.RunEventType("unknown")})
	hub.PublishLive(domain.RunEvent{RunID: "run-1", Type: domain.EventRunStarted})
	hub.PublishLive(domain.RunEvent{Type: domain.EventModelDelta, SchemaVersion: domain.CurrentRunEventSchemaVersion})
	hub.PublishLive(domain.RunEvent{RunID: "run-1", Type: domain.EventModelDelta, SchemaVersion: 0})

	select {
	case item := <-subscription.Events:
		t.Fatalf("invalid publication was delivered: %#v", item)
	default:
	}
}

func TestOutOfOrderPendingOverflowDisconnectsSubscriber(t *testing.T) {
	hub := NewHub(1)
	subscription, err := hub.SnapshotAndSubscribe(context.Background(), "run-1", func() (domain.RunProjectionSnapshot, error) {
		return domain.RunProjectionSnapshot{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	hub.PublishCommitted(runtimeEvent(2, domain.EventRunStarted))
	hub.PublishCommitted(runtimeEvent(3, domain.EventRunCompleted))

	err = <-subscription.Errors
	var typed *SubscriptionError
	if !errors.As(err, &typed) || typed.Code != "event_subscriber_lagged" || typed.RunID != "run-1" {
		t.Fatalf("unexpected lag error: %#v", err)
	}
}

func TestLiveEventBackpressureDisconnectsSubscriber(t *testing.T) {
	hub := NewHub(1)
	subscription, err := hub.SnapshotAndSubscribe(context.Background(), "run-1", func() (domain.RunProjectionSnapshot, error) {
		return domain.RunProjectionSnapshot{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	live := domain.RunEvent{
		RunID: "run-1", Type: domain.EventModelDelta, SchemaVersion: domain.CurrentRunEventSchemaVersion,
		Payload: map[string]any{"delta": "chunk"},
	}
	hub.PublishLive(live)
	hub.PublishLive(live)
	if err := <-subscription.Errors; !errors.Is(err, ErrSubscriberLagged) {
		t.Fatalf("unexpected live lag error: %v", err)
	}
}

func TestRemoveCanReportTerminalSubscriptionError(t *testing.T) {
	hub := NewHub(2)
	subscription, err := hub.SnapshotAndSubscribe(context.Background(), "run-1", func() (domain.RunProjectionSnapshot, error) {
		return domain.RunProjectionSnapshot{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("subscription stopped")
	hub.remove("run-1", 1, wantErr)
	if err := <-subscription.Errors; !errors.Is(err, wantErr) {
		t.Fatalf("reported error=%v want %v", err, wantErr)
	}
	if _, ok := <-subscription.Events; ok {
		t.Fatal("events channel should be closed")
	}
	hub.remove("run-1", 1, nil)
}
