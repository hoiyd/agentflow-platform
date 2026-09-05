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
	"agentflow-platform/apps/api/internal/toolpolicy"
	"agentflow-platform/apps/api/internal/toolprogress"
)

const (
	DefaultExecutionTimeout    = 30 * time.Second
	DefaultMaxResultBytes      = 20_000
	DefaultMaxBatchResultBytes = 8_000
	DefaultMaxConcurrency      = 4
)

type ExecutionRequest struct {
	CallID             string          `json:"call_id,omitempty"`
	RunID              string          `json:"run_id,omitempty"`
	StageID            string          `json:"stage_id,omitempty"`
	TurnID             string          `json:"turn_id,omitempty"`
	IdempotencyKey     string          `json:"idempotency_key,omitempty"`
	Tool               string          `json:"tool"`
	Arguments          json.RawMessage `json:"arguments"`
	DefinitionRevision string          `json:"definition_revision,omitempty"`
	ArgumentsHash      string          `json:"arguments_hash,omitempty"`
	// CredentialScopes contains logical grants from a trusted resolver, never Secret values.
	CredentialScopes []string `json:"-"`
}

type ExecutionResult struct {
	CallID              string                        `json:"call_id,omitempty"`
	Tool                string                        `json:"tool"`
	Arguments           json.RawMessage               `json:"arguments"`
	DefinitionRevision  string                        `json:"-"`
	ArgumentsHash       string                        `json:"-"`
	Result              any                           `json:"result,omitempty"`
	Error               *ExecutionError               `json:"error,omitempty"`
	DurationMS          int64                         `json:"duration_ms"`
	Truncated           bool                          `json:"truncated,omitempty"`
	OriginalResultBytes int                           `json:"original_result_bytes,omitempty"`
	Replayed            bool                          `json:"replayed,omitempty"`
	Artifact            *domain.ToolArtifactReference `json:"artifact,omitempty"`
	ArtifactError       *ExecutionError               `json:"artifact_error,omitempty"`
	PolicyDecision      *toolpolicy.Decision          `json:"-"`
	ProgressDecision    *toolprogress.Decision        `json:"-"`
	ProgressWarning     string                        `json:"progress_warning,omitempty"`
	encodedResult       []byte
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

// PolicyDecisionTracer persists or exports a non-sensitive authorization
// decision. allow_and_log fails closed when this callback is unavailable.
type PolicyDecisionTracer interface {
	ToolPolicyEvaluated(context.Context, ExecutionRequest, toolpolicy.Decision) error
}

// ProgressDecisionTracer records only escalated no-progress decisions. Every
// terminal Tool event also carries the complete bounded decision for recovery.
type ProgressDecisionTracer interface {
	ToolProgressEvaluated(context.Context, ExecutionRequest, toolprogress.Decision)
}

type ToolEffectJournal interface {
	BeginToolEffect(domain.ToolEffectRecord) (domain.ToolEffectRecord, bool, error)
	CompleteToolEffect(idempotencyKey string, result []byte) (domain.ToolEffectRecord, error)
	MarkToolEffectNeedsReconciliation(idempotencyKey string, errorMessage string) (domain.ToolEffectRecord, error)
}

type ExecutorOptions struct {
	DefaultPolicy        ExecutionPolicy
	Tracer               ExecutionTracer
	MaxConcurrency       int
	EffectJournal        ToolEffectJournal
	ArtifactStore        ToolArtifactWriter
	MaxBatchResultBytes  int
	MaxArtifactBytes     int
	ArtifactPreviewBytes int
	ArtifactRetention    time.Duration
	RedactArtifactJSON   func([]byte) ([]byte, int, error)
	ProgressGuard        *toolprogress.Guard
}

type Executor struct {
	catalog             *Catalog
	defaultPolicy       ExecutionPolicy
	tracer              ExecutionTracer
	maxConcurrency      int
	effectJournal       ToolEffectJournal
	artifactGovernor    *resultArtifactGovernor
	maxBatchResultBytes int
	securityPolicy      toolpolicy.Policy
	progressGuard       *toolprogress.Guard
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
	maxBatchResultBytes := options.MaxBatchResultBytes
	if maxBatchResultBytes <= 0 {
		maxBatchResultBytes = DefaultMaxBatchResultBytes
	}
	securityPolicy := catalog.SecurityPolicy()
	return &Executor{
		catalog: catalog, defaultPolicy: policy, tracer: options.Tracer, maxConcurrency: maxConcurrency,
		effectJournal: options.EffectJournal, maxBatchResultBytes: maxBatchResultBytes,
		artifactGovernor: newResultArtifactGovernor(options),
		securityPolicy:   securityPolicy,
		progressGuard:    options.ProgressGuard,
	}
}

// ExecuteBatch preserves input order. A serial or unresolved tool makes the whole
// batch serial; explicitly read-only and distinct keyed groups may run in parallel.
func (e *Executor) ExecuteBatch(ctx context.Context, requests []ExecutionRequest) []ExecutionResult {
	if len(requests) == 0 {
		return nil
	}
	groups, parallel := e.concurrentGroups(requests)
	if !parallel || len(groups) == 1 {
		return e.finishBatch(ctx, requests, e.executeSequential(ctx, requests))
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
					results[item.index] = e.execute(ctx, item.request, false)
				}
			}
		}()
	}
	for _, group := range groups {
		jobs <- group
	}
	close(jobs)
	workers.Wait()
	return e.finishBatch(ctx, requests, results)
}

type indexedExecutionRequest struct {
	index   int
	request ExecutionRequest
}

func (e *Executor) executeSequential(ctx context.Context, requests []ExecutionRequest) []ExecutionResult {
	results := make([]ExecutionResult, len(requests))
	for index, request := range requests {
		results[index] = e.execute(ctx, request, false)
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

func (e *Executor) Execute(ctx context.Context, request ExecutionRequest) ExecutionResult {
	result := e.execute(ctx, request, true)
	result.encodedResult = nil
	return result
}

func (e *Executor) execute(ctx context.Context, request ExecutionRequest, finishTrace bool) (result ExecutionResult) {
	started := time.Now()
	request.Arguments = normalizeArguments(request.Arguments)
	result = ExecutionResult{
		CallID: request.CallID, Tool: request.Tool,
		Arguments: append(json.RawMessage(nil), request.Arguments...),
	}
	binding, call, callErr := e.catalog.prepareCall(request.Tool, request.Arguments)
	request.Arguments = call.Arguments
	result.Arguments = append(json.RawMessage(nil), call.Arguments...)
	if call.DefinitionRevision != "" {
		request.DefinitionRevision = call.DefinitionRevision
		request.ArgumentsHash = call.ArgumentsHash
		result.DefinitionRevision = call.DefinitionRevision
		result.ArgumentsHash = call.ArgumentsHash
	}
	result.Error = callErr
	if e.tracer != nil {
		e.tracer.ToolStarted(ctx, request)
		if finishTrace {
			defer func() { e.tracer.ToolFinished(ctx, result) }()
		}
	}
	defer func() { result.DurationMS = time.Since(started).Milliseconds() }()

	if result.Error != nil {
		if e.progressGuard != nil {
			progressCall := e.progressCall(request, binding)
			if blocked := e.applyProgressBefore(ctx, request, progressCall, &result); blocked {
				return result
			}
			e.observeProgress(ctx, request, binding, progressCall, &result)
		}
		return result
	}
	decision, policyErr := e.evaluateSecurity(ctx, request, binding)
	result.PolicyDecision = &decision
	auditErr := e.tracePolicyDecision(ctx, request, decision)
	if policyErr != nil {
		result.Error = policyErr
		return result
	}
	if decision.AuditRequired() && auditErr != nil {
		result.Error = executionError(ErrorSecurityAudit, "required Tool security audit could not be persisted", auditErr)
		return result
	}
	progressCall := e.progressCall(request, binding)
	if e.applyProgressBefore(ctx, request, progressCall, &result) {
		return result
	}
	if e.progressGuard != nil {
		defer func() { e.observeProgress(ctx, request, binding, progressCall, &result) }()
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
		result.encodedResult = append([]byte(nil), encoded...)
		if len(encoded) > policy.MaxResultBytes {
			result = e.artifactGovernor.govern(ctx, request, result, encoded, policy.MaxResultBytes)
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

func (e *Executor) evaluateSecurity(ctx context.Context, request ExecutionRequest, binding Binding) (toolpolicy.Decision, *ExecutionError) {
	requestedScope := binding.Descriptor.Security.Scope
	if binding.ResolveScope != nil {
		resolved, err := resolveToolScope(ctx, binding.ResolveScope, request.Arguments)
		if err != nil {
			decision := toolpolicy.Decision{
				Action: toolpolicy.ActionDeny, PolicyVersion: e.securityPolicy.Version,
				Reason: "scope_resolution_failed", Capability: binding.Descriptor.Security,
			}
			return decision, executionError(ErrorSecurityScopeInvalid, "Tool security scope could not be resolved", err)
		}
		requestedScope = resolved
	}
	decision := toolpolicy.Evaluate(e.securityPolicy, toolpolicy.Request{
		Tool: request.Tool, Declared: binding.Descriptor.Security, RequestedScope: requestedScope,
		AvailableCredentialScopes: request.CredentialScopes,
	})
	if decision.Allowed {
		return decision, nil
	}
	code := ErrorSecurityPolicyDenied
	switch decision.Reason {
	case "credential_scope_unavailable":
		code = ErrorCredentialScope
	case "binding_scope_expansion", "capability_invalid", "scope_resolution_failed":
		code = ErrorSecurityScopeInvalid
	case "human_approval_required", "approval_policy_not_satisfied":
		code = ErrorApprovalRequired
	}
	return decision, executionError(code, "Tool call denied by security policy: "+decision.Reason, nil)
}

func resolveToolScope(ctx context.Context, resolver ScopeResolver, arguments json.RawMessage) (scope toolpolicy.Scope, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("Tool scope resolver panicked: %v", recovered)
		}
	}()
	return resolver(ctx, arguments)
}

func (e *Executor) tracePolicyDecision(ctx context.Context, request ExecutionRequest, decision toolpolicy.Decision) error {
	tracer, ok := e.tracer.(PolicyDecisionTracer)
	if !ok {
		if decision.AuditRequired() {
			return errors.New("Tool policy decision tracer is unavailable")
		}
		return nil
	}
	return tracer.ToolPolicyEvaluated(ctx, request, decision)
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

func executionError(code ErrorCode, message string, cause error) *ExecutionError {
	return &ExecutionError{Code: code, Message: message, Cause: cause}
}

func invalidArgumentsError(issue *ArgumentValidationIssue) *ExecutionError {
	message := "tool arguments are invalid"
	if issue != nil && issue.Path != "" {
		message += " at " + issue.Path
	}
	return &ExecutionError{Code: ErrorInvalidArgs, Message: message, Argument: issue}
}

func normalizeArguments(args json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(args)) == 0 {
		return json.RawMessage(`{}`)
	}
	return args
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
