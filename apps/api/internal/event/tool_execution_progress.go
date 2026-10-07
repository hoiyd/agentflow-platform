package event

import (
	"context"
	"log"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/tool"
)

type ToolExecutionProgressPayload struct {
	domain.ToolProgressUpdate
	ToolCallID string `json:"tool_call_id"`
	ToolName   string `json:"tool_name"`
}

func (ToolExecutionProgressPayload) supports(t domain.RunEventType) bool {
	return t == domain.EventToolProgress
}

// Persist before the existing Store/Hub publishes. Reconnect and Replay read
// the same committed replacements; no separate live-only stream is invented.
func (t *ToolExecutionTracer) ToolProgressUpdated(ctx context.Context, request tool.ExecutionRequest, update domain.ToolProgressUpdate) {
	if t == nil || t.recorder == nil || t.recorder.store == nil || ctx.Err() != nil {
		return
	}
	t.mu.Lock()
	span, active := t.spans[t.spanKey(request.CallID, request.Tool)]
	t.mu.Unlock()
	if !active || span.Scope.TurnID == "" || (request.RunID != "" && request.RunID != span.Scope.RunID) ||
		(request.StageID != "" && request.StageID != span.Scope.StageID) || (request.TurnID != "" && request.TurnID != span.Scope.TurnID) {
		return
	}
	item, err := NewRunEvent(domain.EventToolProgress, EventMetadata{
		RunID: span.Scope.RunID, ConversationID: span.Scope.ConversationID, StageID: span.Scope.StageID, TurnID: span.Scope.TurnID,
	}, ToolExecutionProgressPayload{ToolCallID: request.CallID, ToolName: request.Tool, ToolProgressUpdate: update})
	if err == nil {
		_, err = t.recorder.store.CreateRunEvent(item)
	}
	if err != nil {
		// Display failure is diagnostic, not Tool failure or a retry instruction.
		log.Printf("tool_progress_record_error run_id=%s call_id=%s error=%q", span.Scope.RunID, request.CallID, err)
	}
}
