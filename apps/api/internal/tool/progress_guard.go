package tool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"agentflow-platform/apps/api/internal/tool/progress"
)

func (e *Executor) progressCall(request ExecutionRequest, binding Binding) progress.Call {
	source := strings.TrimSpace(string(binding.Descriptor.Security.Source))
	if source == "" {
		source = "catalog"
	}
	argumentsHash := request.ArgumentsHash
	if argumentsHash == "" {
		argumentsHash = fallbackArgumentsHash(request.Arguments)
	}
	return progress.Call{
		Source: source, Tool: request.Tool,
		DefinitionRevision: request.DefinitionRevision, ArgumentsHash: argumentsHash,
	}
}

func (e *Executor) applyProgressBefore(ctx context.Context, request ExecutionRequest, call progress.Call, result *ExecutionResult) bool {
	if e.progressGuard == nil {
		return false
	}
	decision := e.progressGuard.Before(call)
	if decision.Action != progress.ActionBlockCall && decision.Action != progress.ActionHaltTurn {
		return false
	}
	result.ProgressDecision = &decision
	code := ErrorProgressBlocked
	message := "Tool call blocked because it repeated without progress"
	if decision.Action == progress.ActionHaltTurn {
		code = ErrorNoProgress
		message = "Turn halted because blocked Tool calls continued without progress"
	}
	result.Error = executionError(code, message, nil)
	e.traceProgressDecision(ctx, request, decision)
	return true
}

func (e *Executor) observeProgress(ctx context.Context, request ExecutionRequest, binding Binding, call progress.Call, result *ExecutionResult) {
	if e.progressGuard == nil || result.ProgressDecision != nil {
		return
	}
	outcome := progress.Outcome{
		EncodedResult: result.encodedResult,
		ReadOnly: binding.Descriptor.Concurrency.Mode == ConcurrencyReadOnly &&
			binding.Descriptor.SideEffect.Mode != SideEffectExternal,
		SuccessfulWrite: result.Error == nil && binding.Descriptor.SideEffect.Mode == SideEffectExternal,
	}
	if result.Error != nil && progressTrackableError(result.Error.Code) {
		info := result.Error.FailureInfo()
		outcome.ErrorCode = string(result.Error.Code)
		outcome.ErrorCategory = string(info.Category)
	}
	decision := e.progressGuard.Observe(call, outcome)
	result.ProgressDecision = &decision
	if decision.Action == progress.ActionWarn {
		result.ProgressWarning = decision.Reason
		e.traceProgressDecision(ctx, request, decision)
	}
}

func (e *Executor) traceProgressDecision(ctx context.Context, request ExecutionRequest, decision progress.Decision) {
	tracer, ok := e.tracer.(ProgressDecisionTracer)
	if ok {
		tracer.ToolProgressEvaluated(ctx, request, decision)
	}
}

func progressTrackableError(code ErrorCode) bool {
	switch code {
	case ErrorToolNotFound, ErrorInvalidArgs, ErrorExecutionFailed,
		ErrorExecutionTimeout, ErrorResultEncoding:
		return true
	default:
		return false
	}
}

func fallbackArgumentsHash(arguments json.RawMessage) string {
	normalized := normalizeArguments(arguments)
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, normalized); err == nil {
		normalized = compacted.Bytes()
	}
	digest := sha256.Sum256(normalized)
	return hex.EncodeToString(digest[:])
}
