package event

import (
	"context"
	"errors"
	"sync"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/toolpolicy"
	"agentflow-platform/apps/api/internal/tools"
)

type ToolExecutionTracer struct {
	recorder *Recorder
	runID    string
	stepID   string
	mu       sync.Mutex
	spans    map[string]Span
}

func (t *ToolExecutionTracer) ToolPolicyEvaluated(ctx context.Context, request tools.ExecutionRequest, decision toolpolicy.Decision) error {
	if t == nil || t.recorder == nil || t.recorder.store == nil {
		return errors.New("Tool policy event recorder is unavailable")
	}
	scope := ScopeFromContext(traceContext(ctx))
	if scope.RunID == "" {
		scope.RunID = t.runID
	}
	if scope.StageID == "" {
		scope.StageID = t.stepID
	}
	if scope.RunID == "" {
		return errors.New("Tool policy event requires Run scope")
	}
	capability := decision.Capability
	event, err := NewRunEvent(domain.EventToolPolicyEvaluated, EventMetadata{
		RunID: scope.RunID, ConversationID: scope.ConversationID, StageID: scope.StageID, TurnID: scope.TurnID,
	}, ToolPolicyPayload{
		ToolCallID: request.CallID, ToolName: request.Tool,
		PolicyVersion: decision.PolicyVersion, RuleID: decision.RuleID,
		Action: string(decision.Action), Allowed: decision.Allowed, Reason: decision.Reason,
		Source: string(capability.Source), SideEffectClass: string(capability.SideEffect),
		RateClass: string(capability.Rate), Reversibility: string(capability.Reversibility),
		Visibility: string(capability.Visibility), ApprovalMode: string(capability.Approval), AuditLevel: string(capability.Audit),
		ResourceScopeCount: len(capability.Scope.Resources), NetworkMode: string(capability.Scope.Network.Mode),
		NetworkTargetCount: len(capability.Scope.Network.Targets), CredentialScopeCount: len(capability.Scope.Credentials),
	})
	if err != nil {
		return err
	}
	_, err = t.recorder.store.CreateRunEvent(event)
	return err
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
	if result.PolicyDecision != nil {
		payload["policy_version"] = result.PolicyDecision.PolicyVersion
		payload["policy_rule_id"] = result.PolicyDecision.RuleID
		payload["policy_action"] = string(result.PolicyDecision.Action)
		payload["policy_reason"] = result.PolicyDecision.Reason
	}
	if result.OriginalResultBytes > 0 {
		payload["original_result_bytes"] = result.OriginalResultBytes
	}
	if result.Artifact != nil {
		payload["artifact"] = result.Artifact
	}
	if result.ArtifactError != nil {
		payload["artifact_error"] = result.ArtifactError.Message
		payload["artifact_error_code"] = string(result.ArtifactError.Code)
	}
	t.recorder.ToolEnd(traceContext(ctx), span, payload)
	if result.Artifact != nil {
		t.recorder.ToolArtifact(t.artifactContext(ctx), domain.EventToolResultPersisted, ToolArtifactPayload{
			ArtifactID: result.Artifact.ID, ToolCallID: result.CallID, ToolName: result.Tool,
			Operation: "persist", ContentHash: result.Artifact.ContentHash, StoredBytes: result.Artifact.ByteSize,
		})
	}
}

func (t *ToolExecutionTracer) artifactContext(ctx context.Context) context.Context {
	base := traceContext(ctx)
	scope := ScopeFromContext(base)
	if scope.RunID == "" {
		scope.RunID = t.runID
	}
	if scope.StageID == "" {
		scope.StageID = t.stepID
	}
	return WithScope(base, scope)
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
