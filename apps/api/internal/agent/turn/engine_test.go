package turn

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
)

type modelStub struct {
	result Result
	err    error
	deltas []string
	events []ModelEvent
}

func (m modelStub) Execute(_ context.Context, _ Request, emit func(ModelEvent)) (Result, error) {
	for _, delta := range m.deltas {
		emit(ModelEvent{Delta: delta})
	}
	for _, event := range m.events {
		emit(event)
	}
	return m.result, m.err
}

func TestEnginePreservesToolLifecycleOrder(t *testing.T) {
	engine := NewEngine(modelStub{
		result: Result{Output: "done"},
		events: []ModelEvent{
			{Type: EventToolStarted, ToolName: "search", ToolCallID: "call-1"},
			{Type: EventToolFinished, ToolName: "search", ToolCallID: "call-1"},
		},
	})
	var events []Event
	_, err := engine.Execute(context.Background(), Request{Input: "work"}, func(event Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []EventType{EventTurnStarted, EventModelStarted, EventToolStarted, EventToolFinished, EventModelFinished, EventTurnCompleted}
	got := make([]EventType, 0, len(events))
	for _, event := range events {
		got = append(got, event.Type)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
	if events[2].ToolName != "search" || events[3].ToolCallID != "call-1" {
		t.Fatalf("tool metadata was not preserved: %#v", events)
	}
}

func TestEngineEmitsSuccessfulLifecycle(t *testing.T) {
	engine := NewEngine(modelStub{result: Result{Output: " done "}, deltas: []string{"do", "ne"}})
	var events []EventType
	result, err := engine.Execute(context.Background(), Request{Input: "work", RunID: "run-1"}, func(event Event) {
		events = append(events, event.Type)
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "done" || result.StopReason != StopCompleted {
		t.Fatalf("unexpected result: %#v", result)
	}
	want := []EventType{EventTurnStarted, EventModelStarted, EventModelDelta, EventModelDelta, EventModelFinished, EventTurnCompleted}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestEngineEmitsFailedLifecycle(t *testing.T) {
	engine := NewEngine(modelStub{err: errors.New("model unavailable")})
	var events []EventType
	result, err := engine.Execute(context.Background(), Request{Input: "work"}, func(event Event) {
		events = append(events, event.Type)
	})
	if err == nil || result.StopReason != StopFailed {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	want := []EventType{EventTurnStarted, EventModelStarted, EventModelFailed, EventTurnFailed}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
}

func TestEngineRejectsEmptyInput(t *testing.T) {
	_, err := NewEngine(modelStub{}).Execute(context.Background(), Request{}, nil)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected ErrInvalidRequest, got %v", err)
	}
}

func TestEngineCheckpointsAnswerBeforeTurnTerminal(t *testing.T) {
	var durable []domain.RunEvent
	engine := NewEngine(modelStub{result: Result{Output: "new answer"}, events: []ModelEvent{
		{Delta: "old commentary "}, {Reset: true}, {Delta: "new "}, {Delta: "answer"},
	}})
	_, err := engine.Execute(context.Background(), Request{Input: "work", RunID: "run-1", TurnID: "turn-1",
		Sink: eventpkg.SinkFunc(func(_ context.Context, item domain.RunEvent) error {
			durable = append(durable, item)
			return nil
		})}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var checkpoints []domain.RunEvent
	for _, item := range durable {
		if item.Type == "model.output_checkpoint" {
			checkpoints = append(checkpoints, item)
		}
	}
	if len(checkpoints) == 0 {
		t.Fatal("streamed output has no durable checkpoint")
	}
	last := checkpoints[len(checkpoints)-1]
	if last.Payload["text"] != "new answer" || last.Payload["status"] != "final" || last.TurnID != "turn-1" {
		t.Fatalf("checkpoint did not replace reset text: %#v", last)
	}
	foundReset := false
	for _, item := range checkpoints {
		if item.Payload["status"] == "retracted" {
			foundReset = true
		}
	}
	if !foundReset || durable[len(durable)-1].Type != domain.EventTurnCompleted {
		t.Fatalf("missing durable reset or terminal ordering: %#v", durable)
	}
}

func TestEngineStopsWhenDurableEventPublishFails(t *testing.T) {
	called := false
	engine := NewEngine(modelStub{result: Result{Output: "should not run"}})
	_, err := engine.Execute(context.Background(), Request{Input: "work", RunID: "run-1", Sink: eventpkg.SinkFunc(func(context.Context, domain.RunEvent) error {
		called = true
		return errors.New("store unavailable")
	})}, nil)
	if err == nil || !called {
		t.Fatalf("expected sink failure, got %v", err)
	}
}
