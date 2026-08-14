package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func (s *FileStore) CreateModelRequestRecord(record domain.ModelRequestRecord) (domain.ModelRequestRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasRunLocked(record.Envelope.RunID) {
		return domain.ModelRequestRecord{}, ErrNotFound("run")
	}
	if err := validateModelRequestRecord(record); err != nil {
		return domain.ModelRequestRecord{}, err
	}
	record.Envelope.Attempt = nextModelRequestAttempt(s.data.ModelRequestRecords, record.Envelope.RunID, record.Envelope.ModelCallID)
	record = cloneModelRequestRecord(record)
	s.data.ModelRequestRecords = append(s.data.ModelRequestRecords, record)
	return cloneModelRequestRecord(record), s.saveLocked()
}

func (s *FileStore) ListModelRequestRecords(runID string) ([]domain.ModelRequestRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasRunLocked(runID) {
		return nil, ErrNotFound("run")
	}
	items := make([]domain.ModelRequestRecord, 0)
	purged := false
	for index, item := range s.data.ModelRequestRecords {
		if item.Envelope.RunID == runID {
			if expireModelRequestCapture(&item, time.Now().UTC()) {
				s.data.ModelRequestRecords[index] = item
				purged = true
			}
			items = append(items, cloneModelRequestRecord(item))
		}
	}
	if purged {
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	}
	sortModelRequestRecords(items)
	return items, nil
}

func validateModelRequestRecord(record domain.ModelRequestRecord) error {
	envelope := record.Envelope
	if strings.TrimSpace(envelope.ID) == "" || strings.TrimSpace(envelope.RunID) == "" ||
		strings.TrimSpace(envelope.ConversationID) == "" || strings.TrimSpace(envelope.ModelCallID) == "" ||
		strings.TrimSpace(envelope.Operation) == "" || strings.TrimSpace(envelope.Model) == "" {
		return errors.New("model request envelope requires id, run, conversation, model call, operation, and model")
	}
	if envelope.PayloadBytes <= 0 || envelope.MessageCount < 0 || envelope.ToolCount < 0 || envelope.CreatedAt.IsZero() {
		return errors.New("model request envelope counters and timestamp are invalid")
	}
	if !validSHA256(envelope.PayloadHash) || !validSHA256(envelope.RuntimeSnapshotHash) {
		return errors.New("model request envelope hashes are invalid")
	}
	if envelope.Parameters == nil {
		return errors.New("model request envelope parameters are required")
	}
	if envelope.SourceTokenBreakdown == nil {
		return errors.New("model request envelope source token breakdown is required")
	}
	for source, tokens := range envelope.SourceTokenBreakdown {
		if strings.TrimSpace(source) == "" || tokens <= 0 {
			return errors.New("model request envelope source token breakdown is invalid")
		}
	}
	capture := record.Capture
	if capture.Mode != domain.ModelRequestCaptureMetadata && capture.Mode != domain.ModelRequestCaptureRedacted && capture.Mode != domain.ModelRequestCaptureFull {
		return errors.New("model request capture mode is invalid")
	}
	if capture.OriginalBytes != envelope.PayloadBytes || capture.StoredBytes != len(capture.Content) || capture.StoredBytes < 0 {
		return errors.New("model request capture byte counts are invalid")
	}
	if capture.Mode == domain.ModelRequestCaptureMetadata && (capture.Content != "" || capture.Redacted || capture.Truncated || capture.ExpiresAt != nil || capture.Expired) {
		return errors.New("metadata-only model request capture cannot contain content or transformation flags")
	}
	if capture.Mode == domain.ModelRequestCaptureRedacted && capture.Content != "" && !capture.Redacted {
		return errors.New("redacted model request capture must declare redaction")
	}
	if capture.Mode == domain.ModelRequestCaptureRedacted && capture.Content != "" && capture.RedactionStrategy == "" {
		return errors.New("redacted model request capture must declare its strategy")
	}
	if capture.Mode == domain.ModelRequestCaptureFull && (capture.Redacted || capture.RedactionStrategy != "" || capture.RedactionCount != 0) {
		return errors.New("full model request capture cannot be redacted")
	}
	if capture.RedactionCount < 0 || capture.Expired && capture.Content != "" {
		return errors.New("model request capture redaction or expiry state is invalid")
	}
	if capture.Content == "" {
		if capture.ContentHash != "" || capture.Reconstructable {
			return errors.New("empty model request capture cannot have a content hash or be reconstructable")
		}
		return nil
	}
	if capture.ExpiresAt == nil {
		return errors.New("stored model request capture requires an expiry")
	}
	if !json.Valid([]byte(capture.Content)) || capture.ContentHash != hashModelRequestBytes([]byte(capture.Content)) {
		return errors.New("model request capture content or hash is invalid")
	}
	if capture.Reconstructable && (capture.Mode != domain.ModelRequestCaptureFull || capture.Redacted || capture.Truncated || capture.ContentHash != envelope.PayloadHash) {
		return errors.New("model request capture reconstructability claim is invalid")
	}
	return nil
}

func expireModelRequestCapture(record *domain.ModelRequestRecord, now time.Time) bool {
	capture := &record.Capture
	if capture.Content == "" || capture.ExpiresAt == nil || now.Before(*capture.ExpiresAt) {
		return false
	}
	capture.Content = ""
	capture.ContentHash = ""
	capture.StoredBytes = 0
	capture.Reconstructable = false
	capture.Expired = true
	return true
}

func nextModelRequestAttempt(items []domain.ModelRequestRecord, runID, modelCallID string) int {
	next := 1
	for _, item := range items {
		if item.Envelope.RunID == runID && item.Envelope.ModelCallID == modelCallID && item.Envelope.Attempt >= next {
			next = item.Envelope.Attempt + 1
		}
	}
	return next
}

func cloneModelRequestRecord(record domain.ModelRequestRecord) domain.ModelRequestRecord {
	encoded, _ := json.Marshal(record)
	var cloned domain.ModelRequestRecord
	_ = json.Unmarshal(encoded, &cloned)
	if cloned.Envelope.Parameters == nil {
		cloned.Envelope.Parameters = map[string]any{}
	}
	return cloned
}

func sortModelRequestRecords(items []domain.ModelRequestRecord) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Envelope.CreatedAt.Equal(items[j].Envelope.CreatedAt) {
			if items[i].Envelope.ModelCallID == items[j].Envelope.ModelCallID {
				return items[i].Envelope.Attempt < items[j].Envelope.Attempt
			}
			return items[i].Envelope.ID < items[j].Envelope.ID
		}
		return items[i].Envelope.CreatedAt.Before(items[j].Envelope.CreatedAt)
	})
}

func validSHA256(value string) bool {
	if !strings.HasPrefix(value, "sha256:") {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && len(decoded) == sha256.Size
}

func hashModelRequestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
