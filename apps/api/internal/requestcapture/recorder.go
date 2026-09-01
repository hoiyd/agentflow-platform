package requestcapture

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	eventpkg "agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/modelrequest"
	"agentflow-platform/apps/api/internal/redaction"
)

const DefaultMaxCaptureBytes = 256 * 1024
const DefaultCaptureRetention = 7 * 24 * time.Hour

type Store interface {
	GetRun(string) (domain.Run, bool, error)
	CreateModelRequestRecord(domain.ModelRequestRecord) (domain.ModelRequestRecord, error)
	CreateRunEvent(domain.RunEvent) (domain.RunEvent, error)
}

type Options struct {
	Mode      domain.ModelRequestCaptureMode
	MaxBytes  int
	Retention time.Duration
}

type Recorder struct {
	store     Store
	mode      domain.ModelRequestCaptureMode
	maxBytes  int
	retention time.Duration
}

func NewRecorder(store Store, options Options) *Recorder {
	mode := NormalizeMode(string(options.Mode))
	maxBytes := options.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxCaptureBytes
	}
	retention := options.Retention
	if retention <= 0 {
		retention = DefaultCaptureRetention
	}
	return &Recorder{store: store, mode: mode, maxBytes: maxBytes, retention: retention}
}

func NormalizeMode(value string) domain.ModelRequestCaptureMode {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(domain.ModelRequestCaptureRedacted):
		return domain.ModelRequestCaptureRedacted
	case string(domain.ModelRequestCaptureFull):
		return domain.ModelRequestCaptureFull
	default:
		return domain.ModelRequestCaptureMetadata
	}
}

func (r *Recorder) Record(ctx context.Context, observation modelrequest.Observation) error {
	if r == nil || r.store == nil {
		return errors.New("model request recorder store is required")
	}
	scope := eventpkg.ScopeFromContext(ctx)
	if strings.TrimSpace(scope.RunID) == "" {
		return nil
	}
	if len(observation.Payload) == 0 || strings.TrimSpace(observation.ModelCallID) == "" ||
		strings.TrimSpace(observation.Operation) == "" || strings.TrimSpace(observation.Model) == "" {
		return errors.New("run-scoped model request requires model call, operation, model, and payload")
	}
	run, ok, err := r.store.GetRun(scope.RunID)
	if err != nil {
		return fmt.Errorf("load model request run: %w", err)
	}
	if !ok || run.RuntimeSnapshot == nil {
		return errors.New("model request run or runtime snapshot not found")
	}
	snapshotHash, err := hashJSON(run.RuntimeSnapshot)
	if err != nil {
		return fmt.Errorf("hash runtime snapshot: %w", err)
	}
	parameters, messageCount, toolCount, err := summarizePayload(observation.Payload)
	if err != nil {
		return fmt.Errorf("summarize model request payload: %w", err)
	}
	now := time.Now().UTC()
	record := domain.ModelRequestRecord{
		Envelope: domain.ModelRequestEnvelope{
			ID: newID("modelreq"), RunID: scope.RunID, ConversationID: run.ConversationID,
			StageID: scope.StageID, TurnID: scope.TurnID, ModelCallID: strings.TrimSpace(observation.ModelCallID),
			Operation: strings.TrimSpace(observation.Operation), Provider: strings.TrimSpace(observation.Provider),
			Model: strings.TrimSpace(observation.Model), ContextManifestID: strings.TrimSpace(observation.ContextManifestID),
			RuntimeSnapshotHash: snapshotHash, PayloadHash: hashBytes(observation.Payload), PayloadBytes: len(observation.Payload),
			Parameters: parameters, SourceTokenBreakdown: cloneTokenBreakdown(observation.SourceTokenBreakdown),
			MessageCount: messageCount, ToolCount: toolCount, CreatedAt: now,
		},
		Capture: capturePayload(observation.Payload, r.mode, r.maxBytes, r.retention, now),
	}
	created, err := r.store.CreateModelRequestRecord(record)
	if err != nil {
		return fmt.Errorf("persist model request envelope: %w", err)
	}
	payload, err := eventpkg.Payload(eventpkg.ModelRequestPreparedPayload{
		RecordID: created.Envelope.ID, ModelCallID: created.Envelope.ModelCallID, Attempt: created.Envelope.Attempt,
		Operation: created.Envelope.Operation, Provider: created.Envelope.Provider, Model: created.Envelope.Model,
		ContextManifestID: created.Envelope.ContextManifestID, RuntimeSnapshotHash: created.Envelope.RuntimeSnapshotHash,
		PayloadHash: created.Envelope.PayloadHash, PayloadBytes: created.Envelope.PayloadBytes,
		CaptureMode: string(created.Capture.Mode), CaptureReconstructable: created.Capture.Reconstructable,
	})
	if err != nil {
		return err
	}
	if _, err := r.store.CreateRunEvent(domain.RunEvent{
		Type: domain.EventModelRequestPrepared, RunID: scope.RunID, ConversationID: run.ConversationID,
		StageID: scope.StageID, TurnID: scope.TurnID, Payload: payload, Timestamp: now,
	}); err != nil {
		return fmt.Errorf("persist model request event: %w", err)
	}
	return nil
}

func capturePayload(payload []byte, mode domain.ModelRequestCaptureMode, maxBytes int, retention time.Duration, now time.Time) domain.ModelRequestCapture {
	capture := domain.ModelRequestCapture{Mode: mode, OriginalBytes: len(payload)}
	if mode == domain.ModelRequestCaptureMetadata {
		return capture
	}
	content := append([]byte(nil), payload...)
	if mode == domain.ModelRequestCaptureRedacted {
		redacted, redactionCount, err := redaction.JSON(content)
		if err != nil {
			capture.Truncated = true
			return capture
		}
		content = redacted
		capture.Redacted = true
		capture.RedactionStrategy = redaction.DeterministicStrategy
		capture.RedactionCount = redactionCount
	}
	if len(content) > maxBytes {
		capture.Truncated = true
		return capture
	}
	capture.Content = string(content)
	capture.ContentHash = hashBytes(content)
	capture.StoredBytes = len(content)
	capture.Reconstructable = mode == domain.ModelRequestCaptureFull && capture.ContentHash == hashBytes(payload)
	expiresAt := now.Add(retention)
	capture.ExpiresAt = &expiresAt
	return capture
}

func summarizePayload(payload []byte) (map[string]any, int, int, error) {
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil, 0, 0, err
	}
	messages, _ := body["messages"].([]any)
	tools, _ := body["tools"].([]any)
	delete(body, "messages")
	delete(body, "tools")
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, 0, 0, err
	}
	redactedJSON, _, err := redaction.JSON(encoded)
	if err != nil {
		return nil, 0, 0, err
	}
	redacted := map[string]any{}
	if string(redactedJSON) == "null" {
		return redacted, len(messages), len(tools), nil
	}
	if err := json.Unmarshal(redactedJSON, &redacted); err != nil {
		return nil, 0, 0, err
	}
	return redacted, len(messages), len(tools), nil
}

func redactJSON(payload []byte) ([]byte, int, error) {
	return redaction.JSON(payload)
}

func redactString(value string, count *int) string {
	redacted, matches := redaction.Text(value)
	if count != nil {
		*count += matches
	}
	return redacted
}

func cloneTokenBreakdown(value map[string]int) map[string]int {
	result := make(map[string]int, len(value))
	for source, tokens := range value {
		if strings.TrimSpace(source) != "" && tokens > 0 {
			result[source] = tokens
		}
	}
	return result
}

func hashJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return hashBytes(encoded), nil
}

func hashBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func newID(prefix string) string {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UTC().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(bytes)
}
