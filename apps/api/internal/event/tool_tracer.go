package event

import (
	"context"
	"sync"

	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/tools"
)

type ToolExecutionTracer struct {
	recorder *Recorder
	runID    string
	stepID   string
	mu       sync.Mutex
	spans    map[string]Span
}

func NewToolExecutionTracer(recorder *Recorder, runID, stepID string) *ToolExecutionTracer {
	return &ToolExecutionTracer{
		recorder: recorder, runID: runID, stepID: stepID,
		spans: map[string]Span{},
	}
}

func (t *ToolExecutionTracer) ToolStarted(ctx context.Context, request tools.ExecutionRequest) {
	if t == nil || t.recorder == nil {
		return
	}
	span := t.recorder.ToolStart(traceContext(ctx), t.runID, t.stepID, map[string]any{
		"tool_call_id":        request.CallID,
		"tool_name":           request.Tool,
		"arguments":           string(request.Arguments),
		"arguments_hash":      request.ArgumentsHash,
		"definition_revision": request.DefinitionRevision,
	})
	t.mu.Lock()
	t.spans[t.spanKey(request.CallID, request.Tool)] = span
	t.mu.Unlock()
}

func (t *ToolExecutionTracer) ToolFinished(ctx context.Context, result tools.ExecutionResult) {
	if t == nil || t.recorder == nil {
		return
	}
	key := t.spanKey(result.CallID, result.Tool)
	t.mu.Lock()
	span := t.spans[key]
	delete(t.spans, key)
	t.mu.Unlock()
	payload := map[string]any{
		"tool_call_id":        result.CallID,
		"tool_name":           result.Tool,
		"arguments":           string(result.Arguments),
		"arguments_hash":      result.ArgumentsHash,
		"definition_revision": result.DefinitionRevision,
		"result":              result.Result,
		"error":               result.ErrorMessage(),
		"truncated":           result.Truncated,
		"replayed":            result.Replayed,
	}
	if result.Error != nil {
		payload["error_code"] = string(result.Error.Code)
		if result.Error.Argument != nil {
			payload["argument_error"] = result.Error.Argument
		}
		payload = failure.Merge(payload, result.Error)
	}
	if result.OriginalResultBytes > 0 {
		payload["original_result_bytes"] = result.OriginalResultBytes
	}
	t.recorder.ToolEnd(traceContext(ctx), span, payload)
}

func (t *ToolExecutionTracer) spanKey(callID, tool string) string {
	if callID != "" {
		return callID
	}
	return tool
}

func traceContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}
