package tool

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

type effectIdentityKey struct{}

// EffectIdentity is installed by Executor only after durable preparation. It
// lets a Binding associate non-sensitive evidence with the same journal record.
type EffectIdentity struct {
	Key, RunID, StageID, TurnID, CallID, Tool, RequestHash, DefinitionRevision string
}

func EffectIdentityFromContext(ctx context.Context) EffectIdentity {
	identity, _ := ctx.Value(effectIdentityKey{}).(EffectIdentity)
	return identity
}

func (e *Executor) beginSideEffect(request ExecutionRequest, mode SideEffectMode, result ExecutionResult) (string, bool, ExecutionResult) {
	if e.effectJournal == nil {
		result.Error = executionError(ErrorIdempotencyRequired, "side-effect tool requires a durable effect journal", nil)
		return "", false, result
	}
	missingOwner := strings.TrimSpace(request.StageID) == "" && strings.TrimSpace(request.TurnID) == ""
	identity := string(mode) + " side-effect tool requires run, stage or turn, and call identity"
	if strings.TrimSpace(request.RunID) == "" || missingOwner || strings.TrimSpace(request.CallID) == "" {
		result.Error = executionError(ErrorIdempotencyRequired, identity, nil)
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
		result.Error = executionError(ErrorExecutionFailed, "side effect was confirmed failed and cannot replay this Tool Call", nil)
		return key, false, result
	}
	if record.Status == domain.ToolEffectCompensated {
		result.Error = executionError(ErrorExecutionFailed, "side effect was compensated and cannot replay this Tool Call", nil)
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

func (e *Executor) effectWasRejected(request ExecutionRequest, key string) bool {
	journal, ok := e.effectJournal.(interface {
		ListToolEffects(string) ([]domain.ToolEffectRecord, error)
	})
	if !ok || key == "" {
		return false
	}
	records, err := journal.ListToolEffects(request.RunID)
	if err != nil {
		return false
	}
	for _, record := range records {
		if record.IdempotencyKey == key {
			return record.Status == domain.ToolEffectFailed && record.RequestHash == request.ArgumentsHash && record.ToolName == request.Tool && record.StageID == request.StageID && record.TurnID == request.TurnID && record.ToolCallID == request.CallID
		}
	}
	return false
}

func sideEffectKey(request ExecutionRequest) string {
	if strings.TrimSpace(request.StageID) == "" {
		// Single Runs have no Stage. Do not reuse a receipt across distinct Turns.
		return "tool_effect_" + hashExecutionIdentity(request.RunID, "turn", request.TurnID, request.CallID, request.Tool)
	}
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
