package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"agentflow-platform/apps/api/internal/budget"
	"agentflow-platform/apps/api/internal/domain"
)

const (
	DefaultExecutionTimeout = 30 * time.Second
	DefaultMaxResultBytes   = 20_000
	DefaultMaxConcurrency   = 4
)

type ExecutionRequest struct {
	CallID         string          `json:"call_id,omitempty"`
	RunID          string          `json:"run_id,omitempty"`
	StageID        string          `json:"stage_id,omitempty"`
	TurnID         string          `json:"turn_id,omitempty"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
	Tool           string          `json:"tool"`
	Arguments      json.RawMessage `json:"arguments"`
}

type ExecutionResult struct {
	CallID              string          `json:"call_id,omitempty"`
	Tool                string          `json:"tool"`
	Arguments           json.RawMessage `json:"arguments"`
	Result              any             `json:"result,omitempty"`
	Error               *ExecutionError `json:"error,omitempty"`
	DurationMS          int64           `json:"duration_ms"`
	Truncated           bool            `json:"truncated,omitempty"`
	OriginalResultBytes int             `json:"original_result_bytes,omitempty"`
	Replayed            bool            `json:"replayed,omitempty"`
}

func (r ExecutionResult) ErrorMessage() string {
	if r.Error == nil {
		return ""
	}
	return r.Error.Message
}

type ExecutionTracer interface {
	ToolStarted(context.Context, ExecutionRequest)
	ToolFinished(context.Context, ExecutionResult)
}

type ToolEffectJournal interface {
	BeginToolEffect(domain.ToolEffectRecord) (domain.ToolEffectRecord, bool, error)
	CompleteToolEffect(idempotencyKey string, result []byte) (domain.ToolEffectRecord, error)
	MarkToolEffectNeedsReconciliation(idempotencyKey string, errorMessage string) (domain.ToolEffectRecord, error)
}

type ExecutorOptions struct {
	DefaultPolicy  ExecutionPolicy
	Tracer         ExecutionTracer
	MaxConcurrency int
	EffectJournal  ToolEffectJournal
}

type Executor struct {
	catalog        *Catalog
	defaultPolicy  ExecutionPolicy
	tracer         ExecutionTracer
	maxConcurrency int
	effectJournal  ToolEffectJournal
}

func NewExecutor(catalog *Catalog, options ExecutorOptions) *Executor {
	policy := options.DefaultPolicy
	if policy.Timeout <= 0 {
		policy.Timeout = DefaultExecutionTimeout
	}
	if policy.MaxResultBytes <= 0 {
		policy.MaxResultBytes = DefaultMaxResultBytes
	}
	maxConcurrency := options.MaxConcurrency
	if maxConcurrency <= 0 {
		maxConcurrency = DefaultMaxConcurrency
	}
	return &Executor{catalog: catalog, defaultPolicy: policy, tracer: options.Tracer, maxConcurrency: maxConcurrency, effectJournal: options.EffectJournal}
}

// ExecuteBatch preserves input order. A serial or unresolved tool makes the whole
// batch serial; explicitly read-only and distinct keyed groups may run in parallel.
func (e *Executor) ExecuteBatch(ctx context.Context, requests []ExecutionRequest) []ExecutionResult {
	if len(requests) == 0 {
		return nil
	}
	groups, parallel := e.concurrentGroups(requests)
	if !parallel || len(groups) == 1 {
		return e.executeSequential(ctx, requests)
	}

	results := make([]ExecutionResult, len(requests))
	jobs := make(chan []indexedExecutionRequest)
	workerCount := min(e.maxConcurrency, len(groups))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for group := range jobs {
				for _, item := range group {
					results[item.index] = e.Execute(ctx, item.request)
				}
			}
		}()
	}
	for _, group := range groups {
		jobs <- group
	}
	close(jobs)
	workers.Wait()
	return results
}

type indexedExecutionRequest struct {
	index   int
	request ExecutionRequest
}

func (e *Executor) executeSequential(ctx context.Context, requests []ExecutionRequest) []ExecutionResult {
	results := make([]ExecutionResult, len(requests))
	for index, request := range requests {
		results[index] = e.Execute(ctx, request)
	}
	return results
}

func (e *Executor) concurrentGroups(requests []ExecutionRequest) ([][]indexedExecutionRequest, bool) {
	if e.catalog == nil {
		return nil, false
	}
	groups := make([][]indexedExecutionRequest, 0, len(requests))
	keyedGroups := make(map[string]int)
	for index, request := range requests {
		binding, ok := e.catalog.Resolve(request.Tool)
		if !ok {
			return nil, false
		}
		item := indexedExecutionRequest{index: index, request: request}
		switch binding.Descriptor.Concurrency.Mode {
		case ConcurrencyReadOnly:
			groups = append(groups, []indexedExecutionRequest{item})
		case ConcurrencyKeyed:
			key, ok := concurrencyKey(binding.Descriptor.Concurrency, request.Arguments)
			if !ok {
				return nil, false
			}
			groupIndex, exists := keyedGroups[key]
			if !exists {
				groupIndex = len(groups)
				keyedGroups[key] = groupIndex
				groups = append(groups, nil)
			}
			groups[groupIndex] = append(groups[groupIndex], item)
		default:
			return nil, false
		}
	}
	return groups, true
}

func concurrencyKey(policy ConcurrencyPolicy, arguments json.RawMessage) (string, bool) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(normalizeArguments(arguments), &object); err != nil {
		return "", false
	}
	raw := bytes.TrimSpace(object[policy.KeyArgument])
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", false
	}
	var scalar any
	if err := json.Unmarshal(raw, &scalar); err != nil {
		return "", false
	}
	switch scalar.(type) {
	case string, float64, bool:
		return strings.TrimSpace(policy.KeyArgument) + ":" + string(raw), true
	default:
		return "", false
	}
}

func (e *Executor) Execute(ctx context.Context, request ExecutionRequest) (result ExecutionResult) {
	started := time.Now()
	request.Arguments = normalizeArguments(request.Arguments)
	result = ExecutionResult{
		CallID: request.CallID, Tool: request.Tool,
		Arguments: append(json.RawMessage(nil), request.Arguments...),
	}
	if e.tracer != nil {
		e.tracer.ToolStarted(ctx, request)
		defer func() { e.tracer.ToolFinished(ctx, result) }()
	}
	defer func() { result.DurationMS = time.Since(started).Milliseconds() }()

	if e.catalog == nil {
		result.Error = executionError(ErrorToolNotFound, fmt.Sprintf("tool %q not found", request.Tool), nil)
		return result
	}
	binding, ok := e.catalog.Resolve(request.Tool)
	if !ok {
		result.Error = executionError(ErrorToolNotFound, fmt.Sprintf("tool %q not found", request.Tool), nil)
		return result
	}
	if !validObjectArguments(request.Arguments) {
		result.Error = executionError(ErrorInvalidArgs, "tool arguments must be a JSON object", nil)
		return result
	}
	if controller := budget.FromContext(ctx); controller != nil {
		if err := controller.RecordToolCall(ctx, budget.ToolCall{
			OperationID: request.CallID, Purpose: budget.PurposeFromContext(ctx), ToolName: request.Tool,
		}); err != nil {
			result.Error = executionError(ErrorBudgetExceeded, err.Error(), err)
			return result
		}
	}
	effectKey := ""
	if binding.Descriptor.SideEffect.Mode == SideEffectExternal {
		var execute bool
		effectKey, execute, result = e.beginSideEffect(request, result)
		if result.Error != nil || !execute {
			return result
		}
	}

	policy := effectivePolicy(e.defaultPolicy, binding.Policy)
	executionCtx, cancel := context.WithTimeout(ctx, policy.Timeout)
	defer cancel()
	type handlerResult struct {
		value any
		err   error
	}
	completed := make(chan handlerResult, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				completed <- handlerResult{err: fmt.Errorf("tool handler panicked: %v", recovered)}
			}
		}()
		value, err := binding.Handler(executionCtx, request.Arguments)
		completed <- handlerResult{value: value, err: err}
	}()

	select {
	case <-executionCtx.Done():
		if ctx.Err() != nil {
			result.Error = executionError(ErrorExecutionCanceled, "tool execution canceled", ctx.Err())
		} else {
			result.Error = executionError(ErrorExecutionTimeout, fmt.Sprintf("tool execution exceeded %s", policy.Timeout), executionCtx.Err())
		}
		e.markSideEffectUncertain(effectKey, result.ErrorMessage())
		return result
	case completed := <-completed:
		if completed.err != nil {
			switch {
			case ctx.Err() != nil:
				result.Error = executionError(ErrorExecutionCanceled, "tool execution canceled", ctx.Err())
			case errors.Is(executionCtx.Err(), context.DeadlineExceeded):
				result.Error = executionError(ErrorExecutionTimeout, fmt.Sprintf("tool execution exceeded %s", policy.Timeout), executionCtx.Err())
			default:
				result.Error = executionError(ErrorExecutionFailed, completed.err.Error(), completed.err)
			}
			e.markSideEffectUncertain(effectKey, result.ErrorMessage())
			return result
		}
		encoded, err := json.Marshal(completed.value)
		if err != nil {
			result.Error = executionError(ErrorResultEncoding, "tool result is not JSON-compatible", err)
			e.markSideEffectUncertain(effectKey, result.ErrorMessage())
			return result
		}
		if len(encoded) > policy.MaxResultBytes {
			result.Result = map[string]any{
				"preview":   truncateUTF8(encoded, policy.MaxResultBytes),
				"truncated": true,
			}
			result.Truncated = true
			result.OriginalResultBytes = len(encoded)
		} else {
			result.Result = completed.value
		}
		if effectKey != "" {
			persisted, err := json.Marshal(result)
			if err != nil {
				result.Error = executionError(ErrorEffectJournal, "encode side-effect result", err)
				e.markSideEffectUncertain(effectKey, result.ErrorMessage())
				return result
			}
			if _, err := e.effectJournal.CompleteToolEffect(effectKey, persisted); err != nil {
				result.Error = executionError(ErrorEffectJournal, "commit side-effect journal: "+err.Error(), err)
				e.markSideEffectUncertain(effectKey, result.ErrorMessage())
				return result
			}
		}
		return result
	}
}

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
		RequestHash: sideEffectRequestHash(request), Status: domain.ToolEffectPrepared,
	})
	if err != nil {
		result.Error = executionError(ErrorEffectJournal, "prepare side-effect journal: "+err.Error(), err)
		return key, false, result
	}
	if execute {
		return key, true, result
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
	return hashExecutionIdentity(request.Tool, string(normalizeArguments(request.Arguments)))
}

func hashExecutionIdentity(parts ...string) string {
	value := strings.Join(parts, "\x00")
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:])
}

func executionError(code ErrorCode, message string, cause error) *ExecutionError {
	return &ExecutionError{Code: code, Message: message, Cause: cause}
}

func normalizeArguments(args json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(args)) == 0 {
		return json.RawMessage(`{}`)
	}
	return args
}

func validObjectArguments(args json.RawMessage) bool {
	var object map[string]any
	return json.Unmarshal(args, &object) == nil && object != nil
}

func effectivePolicy(defaults, override ExecutionPolicy) ExecutionPolicy {
	if override.Timeout > 0 {
		defaults.Timeout = override.Timeout
	}
	if override.MaxResultBytes > 0 {
		defaults.MaxResultBytes = override.MaxResultBytes
	}
	return defaults
}

func truncateUTF8(value []byte, limit int) string {
	if limit >= len(value) {
		return string(value)
	}
	value = value[:limit]
	for len(value) > 0 && !utf8.Valid(value) {
		value = value[:len(value)-1]
	}
	return string(value)
}
