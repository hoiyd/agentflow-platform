package event

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/domain"
)

func TestOutputCheckpointsBoundWritesAndRedactWholePrefix(t *testing.T) {
	var items []domain.RunEvent
	recorder := NewOutputRecorder(SinkFunc(func(_ context.Context, item domain.RunEvent) error {
		items = append(items, item)
		return nil
	}), nil)
	defer recorder.Close()
	for _, delta := range []string{"safe text ", "sk-", "fixtureCredential123456 ", strings.Repeat("字 ", 30000)} {
		if err := recorder.Publish(context.Background(), outputDelta(delta)); err != nil {
			t.Fatal(err)
		}
	}
	if err := recorder.Publish(context.Background(), domain.RunEvent{Type: domain.EventTurnFailed, RunID: "run", TurnID: "turn"}); err != nil {
		t.Fatal(err)
	}
	checkpoints := 0
	for _, item := range items {
		if item.Type != domain.EventModelOutputCheckpoint {
			continue
		}
		checkpoints++
		text, _ := item.Payload["text"].(string)
		if strings.Contains(text, "sk-") || len(text) > domain.MaxPartialOutputBytes || !utf8.ValidString(text) {
			t.Fatalf("unsafe checkpoint: %#v", item)
		}
	}
	if checkpoints == 0 || checkpoints > 4 {
		t.Fatalf("unbounded writes: %d", checkpoints)
	}
	last := items[len(items)-2]
	if last.Payload["status"] != "interrupted" || last.Payload["truncated"] != true {
		t.Fatalf("missing incomplete/cap state: %#v", last)
	}
}

func TestOutputCheckpointPersistenceFailureCancelsWithoutPublication(t *testing.T) {
	want := errors.New("disk unavailable")
	canceled := false
	recorder := NewOutputRecorder(SinkFunc(func(_ context.Context, item domain.RunEvent) error {
		if item.Type == domain.EventModelOutputCheckpoint {
			return want
		}
		return nil
	}), func() { canceled = true })
	if err := recorder.Publish(context.Background(), outputDelta("visible text ")); !errors.Is(err, want) {
		t.Fatalf("failure hidden: %v", err)
	}
	if !canceled {
		t.Fatal("model did not receive cancellation after checkpoint failure")
	}
	if err := recorder.Close(); !errors.Is(err, want) {
		t.Fatalf("close hid failure: %v", err)
	}
	if err := recorder.Close(); !errors.Is(err, want) {
		t.Fatalf("close not idempotent: %v", err)
	}
}

func TestOutputCheckpointTimerCommitsPendingTextWithoutAnotherToken(t *testing.T) {
	var mu sync.Mutex
	var latest string
	committed := make(chan struct{}, 8)
	recorder := NewOutputRecorder(SinkFunc(func(_ context.Context, item domain.RunEvent) error {
		if item.Type == domain.EventModelOutputCheckpoint {
			mu.Lock()
			latest, _ = item.Payload["text"].(string)
			mu.Unlock()
			committed <- struct{}{}
		}
		return nil
	}), nil)
	defer recorder.Close()
	if err := recorder.Publish(context.Background(), outputDelta("first ")); err != nil {
		t.Fatal(err)
	}
	<-committed
	if err := recorder.Publish(context.Background(), outputDelta("second ")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-committed:
	case <-time.After(3 * time.Second):
		t.Fatal("pending output never committed")
	}
	mu.Lock()
	defer mu.Unlock()
	if latest != "first second" {
		t.Fatalf("timer checkpoint=%q", latest)
	}
}

func outputDelta(text string) domain.RunEvent {
	return domain.RunEvent{Type: domain.EventModelDelta, RunID: "run", TurnID: "turn", Payload: map[string]any{"delta": text}}
}

func TestOutputCheckpointsSeparateToolRoundsAndAttempts(t *testing.T) {
	var latest domain.RunEvent
	recorder := NewOutputRecorder(SinkFunc(func(_ context.Context, item domain.RunEvent) error {
		if item.Type == domain.EventModelOutputCheckpoint {
			latest = item
		}
		return nil
	}), nil)
	defer recorder.Close()
	publish := func(call string, attempt int, delta string, reset bool) {
		item := outputDelta(delta)
		item.Payload["model_call_id"], item.Payload["attempt"], item.Payload["reset"] = call, attempt, reset
		if err := recorder.Publish(t.Context(), item); err != nil {
			t.Fatal(err)
		}
	}
	publish("call-1", 1, "discard ", false)
	publish("call-1", 1, "", true)
	publish("call-2", 1, "second ", false)
	if err := recorder.Flush("final"); err != nil {
		t.Fatal(err)
	}
	if latest.Payload["round"] != float64(2) || latest.Payload["text"] != "second" {
		t.Fatalf("round: %#v", latest.Payload)
	}
	publish("call-2", 2, "retry ", false)
	if err := recorder.Flush("interrupted"); err != nil {
		t.Fatal(err)
	}
	if latest.Payload["attempt"] != float64(2) || latest.Payload["text"] != "retry" || latest.Payload["round"] != float64(2) {
		t.Fatalf("attempt concatenated: %#v", latest.Payload)
	}
}

func TestOutputCheckpointPersistsMetadataChangesWithoutNewText(t *testing.T) {
	var items []domain.RunEvent
	recorder := NewOutputRecorder(SinkFunc(func(_ context.Context, item domain.RunEvent) error {
		items = append(items, item)
		return nil
	}), nil)
	defer recorder.Close()
	item := outputDelta("")
	item.Payload["model_call_id"], item.Payload["attempt"], item.Payload["display_text"] = "call", 1, "same text"
	if err := recorder.Publish(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	item.Payload["display_truncated"] = true
	if err := recorder.Publish(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Flush(""); err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[1].Payload["truncated"] != true {
		t.Fatalf("metadata change lost: %#v", items)
	}
	item.Payload["attempt"], item.Payload["display_truncated"] = 2, false
	if err := recorder.Publish(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Flush(""); err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[2].Payload["attempt"] != float64(2) || items[2].Payload["truncated"] == true {
		t.Fatalf("attempt change lost: %#v", items)
	}
}

func TestOutputCheckpointByteThresholdCountsDisplayGrowthOnce(t *testing.T) {
	count := 0
	recorder := NewOutputRecorder(SinkFunc(func(_ context.Context, item domain.RunEvent) error {
		if item.Type == domain.EventModelOutputCheckpoint {
			count++
		}
		return nil
	}), nil)
	defer recorder.Close()
	text := "first "
	publish := func(delta string) {
		text += delta
		item := outputDelta(delta)
		item.Payload["display_text"] = text
		if err := recorder.Publish(t.Context(), item); err != nil {
			t.Fatal(err)
		}
	}
	publish("")
	publish(strings.Repeat("word ", 1700))
	if count != 1 {
		t.Fatalf("same bytes counted as both delta and display: %d", count)
	}
	publish(strings.Repeat("word ", 1700))
	if count != 2 {
		t.Fatalf("byte threshold did not flush: %d", count)
	}
}
