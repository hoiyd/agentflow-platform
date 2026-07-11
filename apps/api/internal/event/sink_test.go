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
