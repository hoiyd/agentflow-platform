package turn

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type modelStub struct {
	result Result
	err    error
	deltas []string
}

func (m modelStub) Execute(_ context.Context, _ Request, emit func(ModelEvent)) (Result, error) {
	for _, delta := range m.deltas {
		emit(ModelEvent{Delta: delta})
	}
	return m.result, m.err
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
	want := []EventType{EventTurnStarted, EventModelStarted, EventTurnFailed}
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
