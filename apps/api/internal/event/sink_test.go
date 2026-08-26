package event

import (
	"context"
	"reflect"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
)

func TestMultiSinkPreservesOrder(t *testing.T) {
	var calls []string
	sink := MultiSink{
		SinkFunc(func(context.Context, domain.RunEvent) error { calls = append(calls, "store"); return nil }),
		SinkFunc(func(context.Context, domain.RunEvent) error { calls = append(calls, "stream"); return nil }),
	}
	if err := sink.Publish(context.Background(), domain.RunEvent{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"store", "stream"}) {
		t.Fatalf("calls = %#v", calls)
	}
}

func TestPayloadUsesJSONFieldNames(t *testing.T) {
	payload, err := Payload(StagePayload{Name: "worker", AgentID: "agent-1", Iteration: 2})
	if err != nil {
		t.Fatal(err)
	}
	if payload["name"] != "worker" || payload["agent_id"] != "agent-1" || payload["iteration"] != float64(2) {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

type sinkStoreStub struct{ calls int }

func (s *sinkStoreStub) CreateRunEvent(item domain.RunEvent) (domain.RunEvent, error) {
	s.calls++
	item.Sequence = int64(s.calls)
	return item, nil
}

type livePublisherStub struct{ item domain.RunEvent }

func (s *livePublisherStub) PublishLive(item domain.RunEvent) { s.item = item }

func TestStoreSinkRoutesNormalizedLiveEventsWithoutPersistence(t *testing.T) {
	backend := &sinkStoreStub{}
	live := &livePublisherStub{}
	sink := StoreSink{Store: backend, Live: live}
	if err := sink.Publish(context.Background(), domain.RunEvent{Type: domain.EventRunProgress, RunID: "run-1"}); err != nil {
		t.Fatal(err)
	}
	if backend.calls != 0 {
		t.Fatalf("live event was persisted %d time(s)", backend.calls)
	}
	if live.item.SchemaVersion != domain.CurrentRunEventSchemaVersion || live.item.Timestamp.IsZero() || live.item.Payload == nil {
		t.Fatalf("live event was not normalized: %#v", live.item)
	}
}
