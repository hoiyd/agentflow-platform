package telemetry

import (
	"encoding/json"
	"math"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"go.opentelemetry.io/otel/attribute"
)

// boundary contains copied identifiers and allowlisted scalar metadata only;
// no raw event map, prompt, reasoning, arguments, results or error text survives.
type boundary struct {
	runID, key, parent, stageParent, name, status string
	at                                            time.Time
	start                                         bool
	attrs                                         []attribute.KeyValue
}

func project(e domain.RunEvent) (boundary, bool) {
	if !identifier(e.RunID) {
		return boundary{}, false
	}
	b := boundary{runID: e.RunID, at: e.Timestamp, key: e.RunID + "/run", status: "completed"}
	switch e.Type {
	case domain.EventRunStarted, domain.EventRunResumed, domain.EventRunWaitingForUser, domain.EventRunCompleted, domain.EventRunFailed, domain.EventRunCanceled:
		b.name = "agentflow.run"
		b.start = e.Type == domain.EventRunStarted || e.Type == domain.EventRunResumed
	case domain.EventStageStarted, domain.EventStageCompleted, domain.EventStageFailed, domain.EventStageCanceled:
		if !identifier(e.StageID) {
			return boundary{}, false
		}
		b.name = "agentflow.stage"
		b.key = e.RunID + "/stage/" + e.StageID
		b.start = e.Type == domain.EventStageStarted
	case domain.EventTurnStarted, domain.EventTurnCompleted, domain.EventTurnFailed, domain.EventTurnCanceled:
		if !identifier(e.TurnID) {
			return boundary{}, false
		}
		b.name = "agentflow.turn"
		b.key = e.RunID + "/turn/" + e.TurnID
		b.start = e.Type == domain.EventTurnStarted
	case domain.EventModelRequestPrepared, domain.EventModelAttemptFinished:
		id, _ := e.Payload["record_id"].(string)
		if !identifier(id) {
			return boundary{}, false
		}
		b.name = "agentflow.model_attempt"
		b.key = e.RunID + "/attempt/" + id
		b.start = e.Type == domain.EventModelRequestPrepared
	case domain.EventToolStarted, domain.EventToolCompleted, domain.EventToolFailed:
		id, _ := e.Payload["tool_call_id"].(string)
		if !identifier(id) {
			return boundary{}, false
		}
		b.name = "agentflow.tool"
		b.key = e.RunID + "/tool/" + id
		b.start = e.Type == domain.EventToolStarted
	default:
		return boundary{}, false
	}
	if b.at.IsZero() {
		b.at = time.Now()
	}
	b.parent = e.RunID + "/run"
	if identifier(e.StageID) {
		b.stageParent = e.RunID + "/stage/" + e.StageID
	}
	if b.name != "agentflow.stage" && identifier(e.StageID) {
		b.parent = e.RunID + "/stage/" + e.StageID
	}
	if (b.name == "agentflow.tool" || b.name == "agentflow.model_attempt") && identifier(e.TurnID) {
		b.parent = e.RunID + "/turn/" + e.TurnID
	}
	if strings.HasSuffix(string(e.Type), ".failed") || e.Payload["status"] == "failed" {
		b.status = "failed"
	}
	if strings.HasSuffix(string(e.Type), ".canceled") || e.Payload["stop_reason"] == "canceled" || e.Payload["error_kind"] == "canceled" {
		b.status = "canceled"
	}
	if e.Type == domain.EventRunWaitingForUser {
		b.status = "waiting_for_user"
	}
	b.attrs = []attribute.KeyValue{attribute.String("agentflow.run.id", e.RunID), attribute.String("agentflow.event.type", string(e.Type)), attribute.Int64("agentflow.event.sequence", e.Sequence)}
	for key, value := range map[string]string{"conversation.id": e.ConversationID, "stage.id": e.StageID, "turn.id": e.TurnID} {
		if identifier(value) {
			b.attrs = append(b.attrs, attribute.String("agentflow."+key, value))
		}
	}
	// These fields are protocol classifications/identifiers, never free text.
	for _, key := range []string{"record_id", "model_call_id", "tool_call_id", "tool_name", "provider", "model", "operation", "error_kind", "error_code", "finish_reason"} {
		if value, ok := e.Payload[key].(string); ok && identifier(value) {
			b.attrs = append(b.attrs, attribute.String("agentflow."+key, value))
		}
	}
	for _, key := range []string{"attempt", "duration_ms", "rate_limit_wait_ms", "model_permit_wait_ms", "owner_capacity_wait_ms", "http_duration_ms", "http_time_to_first_token_ms", "time_to_first_token_ms", "output_tokens_per_second", "prompt_tokens", "completion_tokens", "total_tokens", "http_status"} {
		if value, ok := number(e.Payload[key]); ok {
			b.attrs = append(b.attrs, attribute.Float64("agentflow."+key, value))
		}
	}
	for _, key := range []string{"usage_estimated", "usage_available", "truncated", "replayed"} {
		if value, ok := e.Payload[key].(bool); ok {
			b.attrs = append(b.attrs, attribute.Bool("agentflow."+key, value))
		}
	}
	if !b.start {
		b.attrs = append(b.attrs, attribute.String("agentflow.status", b.status))
	}
	return b, true
}

func identifier(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_-./:", c)) {
			return false
		}
	}
	return true
}
func number(value any) (float64, bool) {
	var n float64
	switch v := value.(type) {
	case int:
		n = float64(v)
	case int64:
		n = float64(v)
	case float64:
		n = v
	case json.Number:
		var err error
		n, err = v.Float64()
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return n, n >= 0 && !math.IsNaN(n) && !math.IsInf(n, 0)
}
