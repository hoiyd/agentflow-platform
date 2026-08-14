package requestcapture

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

// ValidateReconstructability checks the durable relationships required to
// explain and, for full captures, exactly rebuild model requests.
func ValidateReconstructability(run domain.Run, records []domain.ModelRequestRecord, events []domain.RunEvent) error {
	if run.RuntimeSnapshot == nil {
		return errors.New("runtime snapshot is required")
	}
	wantSnapshotHash, err := hashJSON(run.RuntimeSnapshot)
	if err != nil {
		return err
	}
	manifests := manifestsByID(events)
	eventRecords, err := preparedEventRecords(events)
	if err != nil {
		return err
	}
	seenIDs := map[string]bool{}
	attempts := map[string][]bool{}
	for _, record := range records {
		envelope := record.Envelope
		if envelope.RunID != run.ID || envelope.RuntimeSnapshotHash != wantSnapshotHash {
			return fmt.Errorf("model request %s does not match run snapshot", envelope.ID)
		}
		if seenIDs[envelope.ID] {
			return fmt.Errorf("duplicate model request record %s", envelope.ID)
		}
		seenIDs[envelope.ID] = true
		if envelope.Attempt <= 0 {
			return fmt.Errorf("model request %s has invalid attempt", envelope.ID)
		}
		for len(attempts[envelope.ModelCallID]) < envelope.Attempt {
			attempts[envelope.ModelCallID] = append(attempts[envelope.ModelCallID], false)
		}
		if attempts[envelope.ModelCallID][envelope.Attempt-1] {
			return fmt.Errorf("model call %s has duplicate attempt %d", envelope.ModelCallID, envelope.Attempt)
		}
		attempts[envelope.ModelCallID][envelope.Attempt-1] = true
		if envelope.ContextManifestID != "" {
			manifest, exists := manifests[envelope.ContextManifestID]
			if !exists {
				return fmt.Errorf("model request %s references missing context manifest %s", envelope.ID, envelope.ContextManifestID)
			}
			if !sameTokenBreakdown(envelope.SourceTokenBreakdown, selectedTokenBreakdown(manifest)) {
				return fmt.Errorf("model request %s source token breakdown does not match context manifest", envelope.ID)
			}
		}
		if record.Capture.Reconstructable && hashBytes([]byte(record.Capture.Content)) != envelope.PayloadHash {
			return fmt.Errorf("model request %s reconstructable capture hash mismatch", envelope.ID)
		}
		eventRecord, ok := eventRecords[envelope.ID]
		if !ok || eventRecord.PayloadHash != envelope.PayloadHash || eventRecord.ModelCallID != envelope.ModelCallID || eventRecord.Attempt != envelope.Attempt {
			return fmt.Errorf("model request %s has no matching prepared event", envelope.ID)
		}
	}
	for modelCallID, values := range attempts {
		for index, present := range values {
			if !present {
				return fmt.Errorf("model call %s is missing attempt %d", modelCallID, index+1)
			}
		}
	}
	if len(eventRecords) != len(records) {
		return errors.New("model request record and prepared event counts differ")
	}
	return nil
}

type preparedEventRecord struct {
	ModelCallID string
	Attempt     int
	PayloadHash string
}

func preparedEventRecords(events []domain.RunEvent) (map[string]preparedEventRecord, error) {
	result := map[string]preparedEventRecord{}
	for _, item := range events {
		if item.Type != domain.EventModelRequestPrepared {
			continue
		}
		recordID, _ := item.Payload["record_id"].(string)
		modelCallID, _ := item.Payload["model_call_id"].(string)
		payloadHash, _ := item.Payload["payload_hash"].(string)
		if strings.TrimSpace(recordID) == "" {
			continue
		}
		if _, exists := result[recordID]; exists {
			return nil, fmt.Errorf("duplicate model request prepared event %s", recordID)
		}
		result[recordID] = preparedEventRecord{ModelCallID: modelCallID, Attempt: payloadInt(item.Payload["attempt"]), PayloadHash: payloadHash}
	}
	return result, nil
}

func manifestsByID(events []domain.RunEvent) map[string]domain.ContextManifest {
	result := map[string]domain.ContextManifest{}
	for _, item := range events {
		if item.Type != domain.EventContextAssembled {
			continue
		}
		manifest, ok := decodeManifest(item.Payload["manifest"])
		if ok && manifest.ID != "" {
			result[manifest.ID] = manifest
		}
	}
	return result
}

func selectedTokenBreakdown(manifest domain.ContextManifest) map[string]int {
	result := map[string]int{}
	for _, entry := range manifest.Entries {
		if entry.Selected && entry.EstimatedTokens > 0 {
			result[entry.Source] += entry.EstimatedTokens
		}
	}
	return result
}

func sameTokenBreakdown(left, right map[string]int) bool {
	if len(left) != len(right) {
		return false
	}
	for source, tokens := range left {
		if right[source] != tokens {
			return false
		}
	}
	return true
}

func decodeManifest(value any) (domain.ContextManifest, bool) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return domain.ContextManifest{}, false
	}
	var manifest domain.ContextManifest
	if err := json.Unmarshal(encoded, &manifest); err != nil {
		return domain.ContextManifest{}, false
	}
	return manifest, true
}

func payloadInt(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	default:
		return 0
	}
}
