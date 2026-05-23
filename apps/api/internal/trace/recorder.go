package trace

import (
	"context"
	"log"
	"strconv"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

const payloadStringLimit = 20000

type Recorder struct {
	store store.Store
}

type Span struct {
	RunID     string
	StepID    string
	Type      domain.TraceEventType
	StartedAt time.Time
}

func NewRecorder(store store.Store) *Recorder {
	return &Recorder{store: store}
}

func (r *Recorder) LLMStart(ctx context.Context, runID string, stepID string, payload map[string]any) Span {
	return r.start(ctx, runID, stepID, domain.TraceLLMStart, payload)
}

func (r *Recorder) LLMEnd(ctx context.Context, span Span, payload map[string]any) {
	r.end(ctx, span, domain.TraceLLMEnd, payload)
}

func (r *Recorder) ToolStart(ctx context.Context, runID string, stepID string, payload map[string]any) Span {
	return r.start(ctx, runID, stepID, domain.TraceToolStart, payload)
}

func (r *Recorder) ToolEnd(ctx context.Context, span Span, payload map[string]any) {
	r.end(ctx, span, domain.TraceToolEnd, payload)
}

func (r *Recorder) Error(ctx context.Context, runID string, stepID string, payload map[string]any) {
	if r == nil || r.store == nil || strings.TrimSpace(runID) == "" {
		return
	}
	if ctx.Err() != nil {
		return
	}
	event, err := r.store.CreateTraceEvent(domain.TraceEvent{
		RunID:     runID,
		StepID:    strings.TrimSpace(stepID),
		Type:      domain.TraceError,
		Payload:   sanitizePayload(payload),
		Timestamp: time.Now().UTC(),
	})
	if err != nil {
		log.Printf("trace_record_error type=error run_id=%s step_id=%s error=%q", runID, stepID, err.Error())
		return
	}
	logTraceEvent(event)
}

func (r *Recorder) start(ctx context.Context, runID string, stepID string, eventType domain.TraceEventType, payload map[string]any) Span {
	started := time.Now().UTC()
	span := Span{
		RunID:     strings.TrimSpace(runID),
		StepID:    strings.TrimSpace(stepID),
		Type:      eventType,
		StartedAt: started,
	}
	if r == nil || r.store == nil || span.RunID == "" || ctx.Err() != nil {
		return span
	}
	event, err := r.store.CreateTraceEvent(domain.TraceEvent{
		RunID:     span.RunID,
		StepID:    span.StepID,
		Type:      eventType,
		Payload:   sanitizePayload(payload),
		Timestamp: started,
	})
	if err != nil {
		log.Printf("trace_record_error type=%s run_id=%s step_id=%s error=%q", eventType, span.RunID, span.StepID, err.Error())
		return span
	}
	logTraceEvent(event)
	return span
}

func (r *Recorder) end(ctx context.Context, span Span, eventType domain.TraceEventType, payload map[string]any) {
	if r == nil || r.store == nil || span.RunID == "" || ctx.Err() != nil {
		return
	}
	duration := time.Since(span.StartedAt).Milliseconds()
	if duration < 0 {
		duration = 0
	}
	event, err := r.store.CreateTraceEvent(domain.TraceEvent{
		RunID:      span.RunID,
		StepID:     span.StepID,
		Type:       eventType,
		Payload:    sanitizePayload(payload),
		Timestamp:  time.Now().UTC(),
		DurationMS: duration,
	})
	if err != nil {
		log.Printf("trace_record_error type=%s run_id=%s step_id=%s error=%q", eventType, span.RunID, span.StepID, err.Error())
		return
	}
	logTraceEvent(event)
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

func logTraceEvent(event domain.TraceEvent) {
	payload := event.Payload
	fields := []string{
		"trace_event",
		"type=" + string(event.Type),
		"id=" + event.ID,
		"run_id=" + event.RunID,
	}
	if event.StepID != "" {
		fields = append(fields, "step_id="+event.StepID)
	}
	if event.DurationMS > 0 {
		fields = append(fields, "duration_ms="+strconv.FormatInt(event.DurationMS, 10))
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
