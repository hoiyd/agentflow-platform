package event

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

const payloadStringLimit = 20000

type Recorder struct {
	store RunEventStore
}

type Span struct {
	Scope     Scope
	StartedAt time.Time
}

func NewRecorder(store RunEventStore) *Recorder {
	return &Recorder{store: store}
}

func (r *Recorder) LLMStart(ctx context.Context, runID string, stepID string, payload map[string]any) Span {
	return r.start(ctx, runID, stepID, domain.EventModelStarted, payload)
}

func (r *Recorder) LLMEnd(ctx context.Context, span Span, payload map[string]any) {
	r.end(ctx, span, domain.EventModelCompleted, payload)
}

func (r *Recorder) ToolStart(ctx context.Context, runID string, stepID string, payload map[string]any) Span {
	return r.start(ctx, runID, stepID, domain.EventToolStarted, payload)
}

func (r *Recorder) ToolEnd(ctx context.Context, span Span, payload map[string]any) {
	eventType := domain.EventToolCompleted
	if value, _ := payload["error"].(string); strings.TrimSpace(value) != "" {
		eventType = domain.EventToolFailed
	}
	r.end(ctx, span, eventType, payload)
}

func (r *Recorder) ToolArtifact(ctx context.Context, eventType domain.RunEventType, payload ToolArtifactPayload) {
	if r == nil || r.store == nil || ctx == nil || ctx.Err() != nil {
		return
	}
	scope := ScopeFromContext(ctx)
	if scope.RunID == "" {
		return
	}
	event, err := NewRunEvent(eventType, EventMetadata{
		RunID: scope.RunID, ConversationID: scope.ConversationID,
		StageID: scope.StageID, TurnID: scope.TurnID,
	}, payload)
	if err != nil {
		log.Printf("event_record_error type=%s run_id=%s error=%q", eventType, scope.RunID, err.Error())
		return
	}
	if created, err := r.store.CreateRunEvent(event); err != nil {
		log.Printf("event_record_error type=%s run_id=%s error=%q", eventType, scope.RunID, err.Error())
	} else {
		logRunEvent(created)
	}
}

func (r *Recorder) Retrieval(ctx context.Context, runID string, stepID string, payload map[string]any) {
	r.event(ctx, runID, stepID, domain.EventRetrievalCompleted, payload)
}

func (r *Recorder) Error(ctx context.Context, runID string, stepID string, payload map[string]any) {
	r.event(ctx, runID, stepID, domain.EventModelFailed, payload)
}

func (r *Recorder) event(ctx context.Context, runID string, stepID string, eventType domain.RunEventType, payload map[string]any) {
	if r == nil || r.store == nil || strings.TrimSpace(runID) == "" {
		return
	}
	if ctx.Err() != nil {
		return
	}
	scope := recorderScope(ctx, runID, stepID)
	event, err := r.createRunEvent(eventType, scope, payload, time.Now().UTC())
	if err != nil {
		log.Printf("event_record_error type=%s run_id=%s step_id=%s error=%q", eventType, runID, stepID, err.Error())
		return
	}
	logRunEvent(event)
}

func (r *Recorder) start(ctx context.Context, runID string, stepID string, eventType domain.RunEventType, payload map[string]any) Span {
	started := time.Now().UTC()
	span := Span{Scope: recorderScope(ctx, runID, stepID), StartedAt: started}
	if r == nil || r.store == nil || span.Scope.RunID == "" || ctx.Err() != nil {
		return span
	}
	event, err := r.createRunEvent(eventType, span.Scope, payload, started)
	if err != nil {
		log.Printf("event_record_error type=%s run_id=%s stage_id=%s error=%q", eventType, span.Scope.RunID, span.Scope.StageID, err.Error())
		return span
	}
	logRunEvent(event)
	return span
}

func (r *Recorder) end(ctx context.Context, span Span, eventType domain.RunEventType, payload map[string]any) {
	if r == nil || r.store == nil || span.Scope.RunID == "" || ctx.Err() != nil {
		return
	}
	duration := time.Since(span.StartedAt).Milliseconds()
	if duration < 0 {
		duration = 0
	}
	payload = sanitizePayload(payload)
	payload["duration_ms"] = duration
	event, err := r.createRunEvent(eventType, span.Scope, payload, time.Now().UTC())
	if err != nil {
		log.Printf("event_record_error type=%s run_id=%s stage_id=%s error=%q", eventType, span.Scope.RunID, span.Scope.StageID, err.Error())
		return
	}
	logRunEvent(event)
}

func (r *Recorder) createRunEvent(eventType domain.RunEventType, scope Scope, fields map[string]any, timestamp time.Time) (domain.RunEvent, error) {
	event, err := NewRunEvent(eventType, EventMetadata{
		RunID: scope.RunID, ConversationID: scope.ConversationID,
		StageID: scope.StageID, TurnID: scope.TurnID, Timestamp: timestamp,
	}, TracePayload{EventType: eventType, Fields: fields})
	if err != nil {
		return domain.RunEvent{}, err
	}
	return r.store.CreateRunEvent(event)
}

func sanitizePayload(payload map[string]any) map[string]any {
	if payload == nil {
		return map[string]any{}
	}
	sanitized := make(map[string]any, len(payload))
	for key, value := range payload {
		sanitized[key] = sanitizeValue(value)
	}
	return sanitized
}

func sanitizeValue(value any) any {
	switch typed := value.(type) {
	case string:
		if len(typed) <= payloadStringLimit {
			return typed
		}
		return typed[:payloadStringLimit] + "...[truncated]"
	case []string:
		items := make([]string, len(typed))
		for i, item := range typed {
			items[i] = sanitizeValue(item).(string)
		}
		return items
	case []any:
		items := make([]any, len(typed))
		for i, item := range typed {
			items[i] = sanitizeValue(item)
		}
		return items
	case map[string]any:
		return sanitizePayload(typed)
	default:
		return value
	}
}

func recorderScope(ctx context.Context, runID, stepID string) Scope {
	scope := ScopeFromContext(ctx)
	if scope.RunID == "" {
		scope.RunID = strings.TrimSpace(runID)
	}
	if scope.StageID == "" {
		scope.StageID = strings.TrimSpace(stepID)
	}
	return scope
}

func logRunEvent(event domain.RunEvent) {
	payload := event.Payload
	fields := []string{
		"run_event",
		"type=" + string(event.Type),
		"id=" + event.ID,
		"run_id=" + event.RunID,
	}
	if event.StageID != "" {
		fields = append(fields, "stage_id="+event.StageID)
	}
	appendStringField(&fields, payload, "role")
	appendStringField(&fields, payload, "agent_id")
	appendIntField(&fields, payload, "iteration")
	appendStringField(&fields, payload, "model")
	appendStringField(&fields, payload, "tool_name")
	appendStringField(&fields, payload, "tool_call_id")
	appendIntField(&fields, payload, "input_chars")
	appendIntField(&fields, payload, "output_chars")
	appendIntField(&fields, payload, "prompt_tokens")
	appendIntField(&fields, payload, "completion_tokens")
	appendIntField(&fields, payload, "total_tokens")
	appendBoolField(&fields, payload, "token_usage_estimated")
	appendStringField(&fields, payload, "source")
	appendStringField(&fields, payload, "stage")
	appendPreviewField(&fields, payload, "error", 240)
	appendPreviewField(&fields, payload, "input", 240)
	appendPreviewField(&fields, payload, "output", 240)
	log.Print(strings.Join(fields, " "))
}

func appendStringField(fields *[]string, payload map[string]any, key string) {
	value, ok := payload[key].(string)
	if !ok || value == "" {
		return
	}
	*fields = append(*fields, key+"="+strconv.Quote(value))
}

func appendPreviewField(fields *[]string, payload map[string]any, key string, limit int) {
	value, ok := payload[key].(string)
	if !ok || value == "" {
		return
	}
	*fields = append(*fields, key+"_preview="+strconv.Quote(truncateForLog(value, limit)))
}

func appendIntField(fields *[]string, payload map[string]any, key string) {
	switch value := payload[key].(type) {
	case int:
		*fields = append(*fields, key+"="+strconv.Itoa(value))
	case int64:
		*fields = append(*fields, key+"="+strconv.FormatInt(value, 10))
	case float64:
		*fields = append(*fields, key+"="+strconv.Itoa(int(value)))
	}
}

func appendBoolField(fields *[]string, payload map[string]any, key string) {
	value, ok := payload[key].(bool)
	if !ok {
		return
	}
	*fields = append(*fields, key+"="+strconv.FormatBool(value))
}

func truncateForLog(value string, limit int) string {
	value = strings.ReplaceAll(value, "\n", "\\n")
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "...[truncated]"
}
