package store

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func TestValidateModelRequestRecordRejectsInvalidContracts(t *testing.T) {
	base := validModelRequestRecord("run-1", "conversation-1")
	tests := []struct {
		name    string
		mutate  func(*domain.ModelRequestRecord)
		message string
	}{
		{name: "required identity", mutate: func(record *domain.ModelRequestRecord) { record.Envelope.ID = "" }, message: "requires id"},
		{name: "counters", mutate: func(record *domain.ModelRequestRecord) { record.Envelope.PayloadBytes = 0 }, message: "counters"},
		{name: "hash prefix", mutate: func(record *domain.ModelRequestRecord) { record.Envelope.PayloadHash = "invalid" }, message: "hashes"},
		{name: "parameters", mutate: func(record *domain.ModelRequestRecord) { record.Envelope.Parameters = nil }, message: "parameters"},
		{name: "source map", mutate: func(record *domain.ModelRequestRecord) { record.Envelope.SourceTokenBreakdown = nil }, message: "source token breakdown is required"},
		{name: "source entry", mutate: func(record *domain.ModelRequestRecord) { record.Envelope.SourceTokenBreakdown = map[string]int{"": 1} }, message: "source token breakdown is invalid"},
		{name: "capture mode", mutate: func(record *domain.ModelRequestRecord) { record.Capture.Mode = "unknown" }, message: "capture mode"},
		{name: "byte counts", mutate: func(record *domain.ModelRequestRecord) { record.Capture.StoredBytes++ }, message: "byte counts"},
		{name: "metadata flags", mutate: func(record *domain.ModelRequestRecord) { record.Capture.Mode = domain.ModelRequestCaptureMetadata }, message: "metadata-only"},
		{name: "redacted flag", mutate: func(record *domain.ModelRequestRecord) { record.Capture.Mode = domain.ModelRequestCaptureRedacted }, message: "declare redaction"},
		{name: "redaction strategy", mutate: func(record *domain.ModelRequestRecord) {
			record.Capture.Mode = domain.ModelRequestCaptureRedacted
			record.Capture.Redacted = true
		}, message: "declare its strategy"},
		{name: "full redaction", mutate: func(record *domain.ModelRequestRecord) { record.Capture.Redacted = true }, message: "cannot be redacted"},
		{name: "negative redaction count", mutate: func(record *domain.ModelRequestRecord) {
			record.Capture.Mode = domain.ModelRequestCaptureRedacted
			record.Capture.Redacted = true
			record.Capture.RedactionStrategy = "deterministic-v1"
			record.Capture.RedactionCount = -1
			record.Capture.Reconstructable = false
		}, message: "redaction or expiry"},
		{name: "expired content", mutate: func(record *domain.ModelRequestRecord) { record.Capture.Expired = true }, message: "redaction or expiry"},
		{name: "empty content hash", mutate: func(record *domain.ModelRequestRecord) {
			record.Capture.Content = ""
			record.Capture.StoredBytes = 0
			record.Capture.ExpiresAt = nil
		}, message: "empty model request capture"},
		{name: "missing expiry", mutate: func(record *domain.ModelRequestRecord) { record.Capture.ExpiresAt = nil }, message: "requires an expiry"},
		{name: "invalid json", mutate: func(record *domain.ModelRequestRecord) {
			record.Capture.Content = "{"
			record.Capture.StoredBytes = 1
			record.Capture.ContentHash = hashModelRequestBytes([]byte("{"))
		}, message: "content or hash"},
		{name: "content hash", mutate: func(record *domain.ModelRequestRecord) {
			record.Capture.ContentHash = hashModelRequestBytes([]byte(`{"other":true}`))
		}, message: "content or hash"},
		{name: "reconstructability", mutate: func(record *domain.ModelRequestRecord) {
			record.Envelope.PayloadHash = hashModelRequestBytes([]byte(`{"other":true}`))
		}, message: "reconstructability claim"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := cloneModelRequestRecord(base)
			test.mutate(&record)
			err := validateModelRequestRecord(record)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("expected error containing %q, got %v", test.message, err)
			}
		})
	}

	if validSHA256("sha256:not-hex") || validSHA256(strings.Repeat("a", 64)) {
		t.Fatal("invalid SHA-256 values were accepted")
	}
	cloned := cloneModelRequestRecord(domain.ModelRequestRecord{})
	if cloned.Envelope.Parameters == nil {
		t.Fatal("clone should normalize nil parameters")
	}
}

func TestFileStoreModelRequestLifecycleAndOrdering(t *testing.T) {
	path := t.TempDir() + "/agentflow.json"
	fileStore, err := NewFileStore(path)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	conversation, err := fileStore.CreateConversation("model request lifecycle")
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	run, err := fileStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	secondRun, err := fileStore.CreateRunWithContract("agent_planner", conversation.ID, testRuntimeSnapshot(), nil)
	if err != nil {
		t.Fatalf("create second run: %v", err)
	}

	record := validModelRequestRecord(run.ID, conversation.ID)
	created, err := fileStore.CreateModelRequestRecord(record)
	if err != nil || created.Envelope.Attempt != 1 {
		t.Fatalf("create first request: record=%#v err=%v", created, err)
	}
	record.Envelope.ID = "modelreq-retry"
	retry, err := fileStore.CreateModelRequestRecord(record)
	if err != nil || retry.Envelope.Attempt != 2 {
		t.Fatalf("create retry request: record=%#v err=%v", retry, err)
	}
	other := validModelRequestRecord(secondRun.ID, conversation.ID)
	other.Envelope.ID = "modelreq-other-run"
	if _, err := fileStore.CreateModelRequestRecord(other); err != nil {
		t.Fatalf("create other run request: %v", err)
	}

	items, err := fileStore.ListModelRequestRecords(run.ID)
	if err != nil || len(items) != 2 || items[0].Envelope.Attempt != 1 || items[1].Envelope.Attempt != 2 {
		t.Fatalf("unexpected request ordering: items=%#v err=%v", items, err)
	}
	if _, err := fileStore.ListModelRequestRecords("missing-run"); err == nil {
		t.Fatal("expected missing run list error")
	}
	missingRun := validModelRequestRecord("missing-run", conversation.ID)
	if _, err := fileStore.CreateModelRequestRecord(missingRun); err == nil {
		t.Fatal("expected missing run create error")
	}
	invalid := validModelRequestRecord(run.ID, conversation.ID)
	invalid.Envelope.ID = ""
	if _, err := fileStore.CreateModelRequestRecord(invalid); err == nil {
		t.Fatal("expected invalid record create error")
	}

	if err := fileStore.DeleteConversation(conversation.ID); err != nil {
		t.Fatalf("delete conversation: %v", err)
	}
	persisted, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file store: %v", err)
	}
	if strings.Contains(string(persisted), created.Envelope.ID) || strings.Contains(string(persisted), other.Envelope.ID) {
		t.Fatal("conversation deletion left model request records behind")
	}
}

func TestSortModelRequestRecordsEqualTimestamps(t *testing.T) {
	now := time.Now().UTC()
	sameCall := []domain.ModelRequestRecord{
		{Envelope: domain.ModelRequestEnvelope{ID: "second", ModelCallID: "call", Attempt: 2, CreatedAt: now}},
		{Envelope: domain.ModelRequestEnvelope{ID: "first", ModelCallID: "call", Attempt: 1, CreatedAt: now}},
	}
	sortModelRequestRecords(sameCall)
	if sameCall[0].Envelope.Attempt != 1 {
		t.Fatalf("attempt ordering failed: %#v", sameCall)
	}
	differentCalls := []domain.ModelRequestRecord{
		{Envelope: domain.ModelRequestEnvelope{ID: "z", ModelCallID: "call-z", CreatedAt: now}},
		{Envelope: domain.ModelRequestEnvelope{ID: "a", ModelCallID: "call-a", CreatedAt: now}},
	}
	sortModelRequestRecords(differentCalls)
	if differentCalls[0].Envelope.ID != "a" {
		t.Fatalf("stable ID ordering failed: %#v", differentCalls)
	}
}

func validModelRequestRecord(runID, conversationID string) domain.ModelRequestRecord {
	payload := []byte(`{"model":"test","messages":[{"role":"user","content":"hello"}]}`)
	expiresAt := time.Now().UTC().Add(time.Hour)
	return domain.ModelRequestRecord{
		Envelope: domain.ModelRequestEnvelope{
			ID: "modelreq-valid", RunID: runID, ConversationID: conversationID,
			ModelCallID: "call-valid", Operation: "chat.completion", Provider: "test", Model: "test",
			RuntimeSnapshotHash: hashModelRequestBytes([]byte("snapshot")), PayloadHash: hashModelRequestBytes(payload),
			PayloadBytes: len(payload), Parameters: map[string]any{"model": "test"},
			SourceTokenBreakdown: map[string]int{"system": 2}, MessageCount: 1, CreatedAt: time.Now().UTC(),
		},
		Capture: domain.ModelRequestCapture{
			Mode: domain.ModelRequestCaptureFull, Content: string(payload), ContentHash: hashModelRequestBytes(payload),
			OriginalBytes: len(payload), StoredBytes: len(payload), Reconstructable: true, ExpiresAt: &expiresAt,
		},
	}
}

func TestValidModelRequestRecordFixture(t *testing.T) {
	record := validModelRequestRecord("run", "conversation")
	if err := validateModelRequestRecord(record); err != nil {
		t.Fatalf("fixture is invalid: %v", err)
	}
	encoded, err := json.Marshal(record)
	if err != nil || !json.Valid(encoded) {
		t.Fatalf("fixture does not round trip: %v", err)
	}
}
