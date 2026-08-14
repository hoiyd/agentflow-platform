package requestcapture

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/modelrequest"
	"agentflow-platform/apps/api/internal/store"
)

func TestFullCaptureRoundTripAndReconstructability(t *testing.T) {
	fileStore, run, path := newCaptureTestRun(t)
	manifestID := "ctx-test"
	if _, err := fileStore.CreateRunEvent(domain.RunEvent{
		Type: domain.EventContextAssembled, RunID: run.ID, ConversationID: run.ConversationID,
		Payload: map[string]any{"manifest": domain.ContextManifest{
			ID: manifestID, RunID: run.ID, ModelCallID: "call-1",
			Entries: []domain.ContextManifestEntry{{Source: "system", Selected: true, EstimatedTokens: 7}},
		}},
	}); err != nil {
		t.Fatalf("create manifest event: %v", err)
	}
	recorder := NewRecorder(fileStore, Options{Mode: domain.ModelRequestCaptureFull, MaxBytes: 4096})
	ctx := eventpkg.WithScope(context.Background(), eventpkg.Scope{
		RunID: run.ID, ConversationID: run.ConversationID, StageID: "stage-1", TurnID: "turn-1",
	})
	payload := []byte(`{"messages":[{"role":"user","content":"hello"}],"model":"test-model","temperature":0.2,"tools":[{"type":"function"}]}`)
	observation := modelrequest.Observation{
		ModelCallID: "call-1", Operation: "chat.completion", Provider: "test", Model: "test-model",
		ContextManifestID: manifestID, SourceTokenBreakdown: map[string]int{"system": 7}, Payload: payload,
	}
	if err := recorder.Record(ctx, observation); err != nil {
		t.Fatalf("record request: %v", err)
	}
	if err := recorder.Record(ctx, observation); err != nil {
		t.Fatalf("record retry: %v", err)
	}

	reopened, err := store.NewFileStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	records, err := reopened.ListModelRequestRecords(run.ID)
	if err != nil || len(records) != 2 {
		t.Fatalf("list records: records=%#v err=%v", records, err)
	}
	if records[0].Envelope.Attempt != 1 || records[1].Envelope.Attempt != 2 ||
		!records[0].Capture.Reconstructable || records[0].Capture.Content != string(payload) ||
		records[0].Envelope.MessageCount != 1 || records[0].Envelope.ToolCount != 1 ||
		records[0].Envelope.SourceTokenBreakdown["system"] != 7 || records[0].Capture.ExpiresAt == nil {
		t.Fatalf("unexpected captures: %#v", records)
	}
	events, err := reopened.ListRunEvents(run.ID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	loadedRun, _, _ := reopened.GetRun(run.ID)
	if err := ValidateReconstructability(loadedRun, records, events); err != nil {
		t.Fatalf("validate reconstructability: %v", err)
	}
}

func TestRedactedAndMetadataCaptureNeverPersistSecrets(t *testing.T) {
	for _, mode := range []domain.ModelRequestCaptureMode{domain.ModelRequestCaptureMetadata, domain.ModelRequestCaptureRedacted} {
		t.Run(string(mode), func(t *testing.T) {
			fileStore, run, _ := newCaptureTestRun(t)
			recorder := NewRecorder(fileStore, Options{Mode: mode, MaxBytes: 4096})
			ctx := eventpkg.WithScope(context.Background(), eventpkg.Scope{RunID: run.ID, ConversationID: run.ConversationID})
			secret := "sk-supersecret123456"
			payload := []byte(`{"api_key":"` + secret + `","messages":[{"role":"user","content":"token=` + secret + `"}],"model":"test-model"}`)
			if err := recorder.Record(ctx, modelrequest.Observation{
				ModelCallID: "call-secret", Operation: "chat.completion", Provider: "test", Model: "test-model", Payload: payload,
			}); err != nil {
				t.Fatalf("record request: %v", err)
			}
			records, _ := fileStore.ListModelRequestRecords(run.ID)
			encoded := records[0].Capture.Content
			parameters := records[0].Envelope.Parameters
			if strings.Contains(encoded, secret) || strings.Contains(string(mustJSON(parameters)), secret) || records[0].Capture.Reconstructable {
				t.Fatalf("secret or reconstructability leaked in %s capture: %#v", mode, records[0])
			}
			if mode == domain.ModelRequestCaptureMetadata && encoded != "" {
				t.Fatalf("metadata capture stored content: %q", encoded)
			}
			if mode == domain.ModelRequestCaptureRedacted && (!records[0].Capture.Redacted || !strings.Contains(encoded, "[REDACTED]")) {
				t.Fatalf("redacted capture missing markers: %#v", records[0].Capture)
			}
			if mode == domain.ModelRequestCaptureRedacted && (records[0].Capture.RedactionStrategy != "deterministic-v1" || records[0].Capture.RedactionCount == 0) {
				t.Fatalf("redacted capture missing metadata: %#v", records[0].Capture)
			}
		})
	}
}

func TestExpiredCaptureContentIsPurgedButEnvelopeRemains(t *testing.T) {
	fileStore, run, path := newCaptureTestRun(t)
	recorder := NewRecorder(fileStore, Options{Mode: domain.ModelRequestCaptureFull, MaxBytes: 4096, Retention: time.Nanosecond})
	ctx := eventpkg.WithScope(context.Background(), eventpkg.Scope{RunID: run.ID, ConversationID: run.ConversationID})
	payload := []byte(`{"messages":[{"role":"user","content":"short-lived"}],"model":"test-model"}`)
	if err := recorder.Record(ctx, modelrequest.Observation{
		ModelCallID: "call-expired", Operation: "chat.completion", Provider: "test", Model: "test-model", Payload: payload,
	}); err != nil {
		t.Fatalf("record request: %v", err)
	}
	records, err := fileStore.ListModelRequestRecords(run.ID)
	if err != nil || len(records) != 1 {
		t.Fatalf("list records: records=%#v err=%v", records, err)
	}
	if !records[0].Capture.Expired || records[0].Capture.Content != "" || records[0].Capture.ContentHash != "" || records[0].Capture.Reconstructable {
		t.Fatalf("expired content was not purged: %#v", records[0])
	}
	if records[0].Envelope.PayloadHash == "" || records[0].Envelope.PayloadBytes != len(payload) {
		t.Fatalf("expiration removed durable envelope metadata: %#v", records[0].Envelope)
	}
	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file store: %v", err)
	}
	if strings.Contains(string(persisted), "short-lived") {
		t.Fatal("expired capture content remains in the file store")
	}
}

func TestCaptureSizeLimitKeepsEnvelopeWithoutPartialJSON(t *testing.T) {
	fileStore, run, _ := newCaptureTestRun(t)
	recorder := NewRecorder(fileStore, Options{Mode: domain.ModelRequestCaptureFull, MaxBytes: 32})
	ctx := eventpkg.WithScope(context.Background(), eventpkg.Scope{RunID: run.ID, ConversationID: run.ConversationID})
	payload := []byte(`{"messages":[{"role":"user","content":"this payload is deliberately larger than the capture limit"}],"model":"test-model"}`)
	if err := recorder.Record(ctx, modelrequest.Observation{
		ModelCallID: "call-large", Operation: "chat.completion", Provider: "test", Model: "test-model", Payload: payload,
	}); err != nil {
		t.Fatalf("record request: %v", err)
	}
	records, err := fileStore.ListModelRequestRecords(run.ID)
	if err != nil || len(records) != 1 {
		t.Fatalf("list records: records=%#v err=%v", records, err)
	}
	capture := records[0].Capture
	if !capture.Truncated || capture.Content != "" || capture.ContentHash != "" || capture.StoredBytes != 0 || capture.Reconstructable {
		t.Fatalf("oversized capture should preserve metadata without partial JSON: %#v", capture)
	}
}

func TestValidateReconstructabilityRejectsDuplicatePreparedEvent(t *testing.T) {
	fileStore, run, _ := newCaptureTestRun(t)
	recorder := NewRecorder(fileStore, Options{Mode: domain.ModelRequestCaptureFull, MaxBytes: 4096})
	ctx := eventpkg.WithScope(context.Background(), eventpkg.Scope{RunID: run.ID, ConversationID: run.ConversationID})
	if err := recorder.Record(ctx, modelrequest.Observation{
		ModelCallID: "call-duplicate", Operation: "chat.completion", Provider: "test", Model: "test-model",
		Payload: []byte(`{"messages":[{"role":"user","content":"hello"}],"model":"test-model"}`),
	}); err != nil {
		t.Fatalf("record request: %v", err)
	}
	records, _ := fileStore.ListModelRequestRecords(run.ID)
	events, _ := fileStore.ListRunEvents(run.ID)
	for _, item := range events {
		if item.Type == domain.EventModelRequestPrepared {
			duplicate := item
			duplicate.ID = "duplicate-event"
			events = append(events, duplicate)
			break
		}
	}
	if err := ValidateReconstructability(run, records, events); err == nil || !strings.Contains(err.Error(), "duplicate model request prepared event") {
		t.Fatalf("expected duplicate prepared event error, got %v", err)
	}
}

func newCaptureTestRun(t *testing.T) (*store.FileStore, domain.Run, string) {
	t.Helper()
	path := t.TempDir() + "/agentflow.json"
	fileStore, err := store.NewFileStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("capture test")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := fileStore.CreateRun("agent_planner", conversation.ID, domain.RuntimeSnapshot{
		SchemaVersion: domain.CurrentRuntimeSnapshotVersion, Mode: "single",
		Agent: domain.RuntimeAgentSnapshot{ID: "agent_planner"}, Model: domain.RuntimeModelSnapshot{Provider: "test", Model: "test-model"},
		ContextAssembly: domain.ContextAssemblyConfig{AssemblerVersion: "context-assembler-v1"}, RunBudget: &domain.RuntimeRunBudget{},
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	return fileStore, run, path
}

func mustJSON(value any) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}
