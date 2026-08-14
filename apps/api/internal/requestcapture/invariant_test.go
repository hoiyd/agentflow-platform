package requestcapture

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/modelrequest"
)

func TestValidateReconstructabilityRejectsBrokenRelationships(t *testing.T) {
	baseRun, baseRecords, baseEvents := reconstructabilityFixture(t)
	tests := []struct {
		name    string
		mutate  func(*domain.Run, *[]domain.ModelRequestRecord, *[]domain.RunEvent)
		message string
	}{
		{name: "missing runtime snapshot", mutate: func(run *domain.Run, _ *[]domain.ModelRequestRecord, _ *[]domain.RunEvent) { run.RuntimeSnapshot = nil }, message: "runtime snapshot is required"},
		{name: "snapshot mismatch", mutate: func(_ *domain.Run, records *[]domain.ModelRequestRecord, _ *[]domain.RunEvent) {
			(*records)[0].Envelope.RuntimeSnapshotHash = hashBytes([]byte("other"))
		}, message: "does not match run snapshot"},
		{name: "duplicate record", mutate: func(_ *domain.Run, records *[]domain.ModelRequestRecord, _ *[]domain.RunEvent) {
			*records = append(*records, (*records)[0])
		}, message: "duplicate model request record"},
		{name: "invalid attempt", mutate: func(_ *domain.Run, records *[]domain.ModelRequestRecord, _ *[]domain.RunEvent) {
			(*records)[0].Envelope.Attempt = 0
		}, message: "invalid attempt"},
		{name: "duplicate attempt", mutate: func(_ *domain.Run, records *[]domain.ModelRequestRecord, events *[]domain.RunEvent) {
			duplicate := (*records)[0]
			duplicate.Envelope.ID = "modelreq-second"
			*records = append(*records, duplicate)
			*events = append(*events, preparedEventFor(duplicate))
		}, message: "duplicate attempt"},
		{name: "missing manifest", mutate: func(_ *domain.Run, _ *[]domain.ModelRequestRecord, events *[]domain.RunEvent) {
			*events = filterEvents(*events, domain.EventContextAssembled)
		}, message: "references missing context manifest"},
		{name: "source mismatch", mutate: func(_ *domain.Run, records *[]domain.ModelRequestRecord, _ *[]domain.RunEvent) {
			(*records)[0].Envelope.SourceTokenBreakdown["system"]++
		}, message: "source token breakdown"},
		{name: "capture mismatch", mutate: func(_ *domain.Run, records *[]domain.ModelRequestRecord, _ *[]domain.RunEvent) {
			(*records)[0].Capture.Content = `{"changed":true}`
		}, message: "capture hash mismatch"},
		{name: "missing prepared event", mutate: func(_ *domain.Run, _ *[]domain.ModelRequestRecord, events *[]domain.RunEvent) {
			*events = filterEvents(*events, domain.EventModelRequestPrepared)
		}, message: "no matching prepared event"},
		{name: "attempt gap", mutate: func(_ *domain.Run, records *[]domain.ModelRequestRecord, events *[]domain.RunEvent) {
			(*records)[0].Envelope.Attempt = 2
			for index := range *events {
				if (*events)[index].Type == domain.EventModelRequestPrepared {
					(*events)[index].Payload["attempt"] = 2
				}
			}
		}, message: "missing attempt 1"},
		{name: "event count", mutate: func(_ *domain.Run, records *[]domain.ModelRequestRecord, _ *[]domain.RunEvent) { *records = nil }, message: "counts differ"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			run, records, events := cloneReconstructabilityFixture(baseRun, baseRecords, baseEvents)
			test.mutate(&run, &records, &events)
			err := ValidateReconstructability(run, records, events)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("expected error containing %q, got %v", test.message, err)
			}
		})
	}
}

func TestInvariantDecodersAndComparators(t *testing.T) {
	if sameTokenBreakdown(map[string]int{"system": 1}, map[string]int{}) {
		t.Fatal("different map lengths should not match")
	}
	if sameTokenBreakdown(map[string]int{"system": 1}, map[string]int{"system": 2}) {
		t.Fatal("different token counts should not match")
	}
	if _, ok := decodeManifest(make(chan int)); ok {
		t.Fatal("unsupported manifest value should fail encoding")
	}
	if _, ok := decodeManifest("not-an-object"); ok {
		t.Fatal("non-object manifest should fail decoding")
	}

	tests := []struct {
		value any
		want  int
	}{
		{value: 1, want: 1},
		{value: int64(2), want: 2},
		{value: float64(3), want: 3},
		{value: json.Number("4"), want: 4},
		{value: "5", want: 0},
	}
	for _, test := range tests {
		if got := payloadInt(test.value); got != test.want {
			t.Fatalf("payloadInt(%#v)=%d want %d", test.value, got, test.want)
		}
	}
	if _, err := hashJSON(make(chan int)); err == nil {
		t.Fatal("hashJSON should reject unsupported values")
	}
}

func reconstructabilityFixture(t *testing.T) (domain.Run, []domain.ModelRequestRecord, []domain.RunEvent) {
	t.Helper()
	fileStore, run, _ := newCaptureTestRun(t)
	manifest := domain.ContextManifest{
		ID: "ctx-invariant", RunID: run.ID, ModelCallID: "call-invariant",
		Entries: []domain.ContextManifestEntry{{Source: "system", Selected: true, EstimatedTokens: 3}},
	}
	if _, err := fileStore.CreateRunEvent(domain.RunEvent{
		Type: domain.EventContextAssembled, RunID: run.ID, ConversationID: run.ConversationID,
		Payload: map[string]any{"manifest": manifest},
	}); err != nil {
		t.Fatalf("create context event: %v", err)
	}
	recorder := NewRecorder(fileStore, Options{Mode: domain.ModelRequestCaptureFull})
	ctx := eventpkg.WithScope(context.Background(), eventpkg.Scope{RunID: run.ID, ConversationID: run.ConversationID})
	if err := recorder.Record(ctx, modelrequest.Observation{
		ModelCallID: "call-invariant", Operation: "chat.completion", Provider: "test", Model: "test-model",
		ContextManifestID: manifest.ID, SourceTokenBreakdown: map[string]int{"system": 3},
		Payload: []byte(`{"model":"test-model","messages":[{"role":"user","content":"hello"}]}`),
	}); err != nil {
		t.Fatalf("record request: %v", err)
	}
	records, _ := fileStore.ListModelRequestRecords(run.ID)
	events, _ := fileStore.ListRunEvents(run.ID)
	return run, records, events
}

func cloneReconstructabilityFixture(run domain.Run, records []domain.ModelRequestRecord, events []domain.RunEvent) (domain.Run, []domain.ModelRequestRecord, []domain.RunEvent) {
	encoded, _ := json.Marshal(struct {
		Run     domain.Run
		Records []domain.ModelRequestRecord
		Events  []domain.RunEvent
	}{Run: run, Records: records, Events: events})
	var cloned struct {
		Run     domain.Run
		Records []domain.ModelRequestRecord
		Events  []domain.RunEvent
	}
	_ = json.Unmarshal(encoded, &cloned)
	return cloned.Run, cloned.Records, cloned.Events
}

func preparedEventFor(record domain.ModelRequestRecord) domain.RunEvent {
	return domain.RunEvent{
		Type: domain.EventModelRequestPrepared, RunID: record.Envelope.RunID, ConversationID: record.Envelope.ConversationID,
		Payload: map[string]any{
			"record_id": record.Envelope.ID, "model_call_id": record.Envelope.ModelCallID,
			"attempt": record.Envelope.Attempt, "payload_hash": record.Envelope.PayloadHash,
		},
	}
}

func filterEvents(events []domain.RunEvent, excluded domain.RunEventType) []domain.RunEvent {
	result := make([]domain.RunEvent, 0, len(events))
	for _, item := range events {
		if item.Type != excluded {
			result = append(result, item)
		}
	}
	return result
}
