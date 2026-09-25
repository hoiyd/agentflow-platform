package tool

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

func (e *Executor) beginSideEffect(request ExecutionRequest, result ExecutionResult) (string, bool, ExecutionResult) {
	if e.effectJournal == nil {
		result.Error = executionError(ErrorIdempotencyRequired, "external side-effect tool requires a durable effect journal", nil)
		return "", false, result
	}
	if strings.TrimSpace(request.RunID) == "" || strings.TrimSpace(request.StageID) == "" || strings.TrimSpace(request.CallID) == "" {
		result.Error = executionError(ErrorIdempotencyRequired, "external side-effect tool requires run, stage, and call identity", nil)
		return "", false, result
	}
	key := strings.TrimSpace(request.IdempotencyKey)
	if key == "" {
		key = sideEffectKey(request)
	}
	record, execute, err := e.effectJournal.BeginToolEffect(domain.ToolEffectRecord{
		IdempotencyKey: key, RunID: request.RunID, StageID: request.StageID,
		TurnID: request.TurnID, ToolCallID: request.CallID, ToolName: request.Tool,
		DefinitionRevision: request.DefinitionRevision,
		RequestHash:        sideEffectRequestHash(request), Status: domain.ToolEffectPrepared,
	})
	if err != nil {
		result.Error = executionError(ErrorEffectJournal, "prepare side-effect journal: "+err.Error(), err)
		return key, false, result
	}
	if execute {
		return key, true, result
	}
	if record.Status == domain.ToolEffectFailed {
		result.Error = executionError(ErrorExecutionFailed, "external side effect was confirmed failed and cannot replay this Tool Call", nil)
		return key, false, result
	}
	if record.Status == domain.ToolEffectCompensated {
		result.Error = executionError(ErrorExecutionFailed, "external side effect was compensated and cannot replay this Tool Call", nil)
		return key, false, result
	}
	if record.Status != domain.ToolEffectCommitted {
		result.Error = executionError(ErrorEffectReconciliation, "side effect has an uncertain prior attempt and requires reconciliation", nil)
		return key, false, result
	}
	var replayed ExecutionResult
	if err := json.Unmarshal(record.Result, &replayed); err != nil {
		result.Error = executionError(ErrorEffectJournal, "decode committed side-effect result", err)
		return key, false, result
	}
	replayed.DefinitionRevision = request.DefinitionRevision
	replayed.ArgumentsHash = request.ArgumentsHash
	replayed.Replayed = true
	return key, false, replayed
}

func (e *Executor) markSideEffectUncertain(key string, message string) {
	if key == "" || e.effectJournal == nil {
		return
	}
	_, _ = e.effectJournal.MarkToolEffectNeedsReconciliation(key, message)
}

func sideEffectKey(request ExecutionRequest) string {
	return "tool_effect_" + hashExecutionIdentity(request.RunID, request.StageID, request.CallID, request.Tool)
}

func sideEffectRequestHash(request ExecutionRequest) string {
	if request.ArgumentsHash != "" {
		return request.ArgumentsHash
	}
	return hashExecutionIdentity(request.Tool, string(normalizeArguments(request.Arguments)))
}

func hashExecutionIdentity(parts ...string) string {
	value := strings.Join(parts, "\x00")
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:])
}
