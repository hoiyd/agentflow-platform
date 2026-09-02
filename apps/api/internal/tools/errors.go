package tools

import "agentflow-platform/apps/api/internal/failure"

type ErrorCode string

const (
	ErrorToolNotFound         ErrorCode = "tool_not_found"
	ErrorInvalidArgs          ErrorCode = "invalid_arguments"
	ErrorExecutionFailed      ErrorCode = "execution_failed"
	ErrorExecutionTimeout     ErrorCode = "execution_timeout"
	ErrorExecutionCanceled    ErrorCode = "execution_canceled"
	ErrorResultEncoding       ErrorCode = "result_encoding_failed"
	ErrorBudgetExceeded       ErrorCode = "budget_exceeded"
	ErrorIdempotencyRequired  ErrorCode = "idempotency_required"
	ErrorEffectReconciliation ErrorCode = "side_effect_reconciliation_required"
	ErrorEffectJournal        ErrorCode = "side_effect_journal_failed"
	ErrorArtifactUnavailable  ErrorCode = "artifact_unavailable"
	ErrorSecurityPolicyDenied ErrorCode = "security_policy_denied"
	ErrorSecurityScopeInvalid ErrorCode = "security_scope_invalid"
	ErrorCredentialScope      ErrorCode = "credential_scope_unavailable"
	ErrorApprovalRequired     ErrorCode = "approval_required"
	ErrorSecurityAudit        ErrorCode = "security_audit_failed"
	ErrorProgressBlocked      ErrorCode = "tool_progress_blocked"
	ErrorNoProgress           ErrorCode = "turn_no_progress"
)

type ExecutionError struct {
	Code     ErrorCode                `json:"code"`
	Message  string                   `json:"message"`
	Argument *ArgumentValidationIssue `json:"argument,omitempty"`
	Cause    error                    `json:"-"`
}

func (e *ExecutionError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *ExecutionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *ExecutionError) FailureInfo() failure.Info {
	if e == nil {
		return failure.Info{Code: "tool_execution_failed", Source: "tool", Category: failure.CategoryInternal}
	}
	info := failure.Info{Code: string(e.Code), Source: "tool", Category: failure.CategoryExecution}
	switch e.Code {
	case ErrorToolNotFound:
		info.Category = failure.CategoryNotFound
	case ErrorInvalidArgs:
		info.Category = failure.CategoryValidation
	case ErrorExecutionTimeout:
		info.Category, info.Retryable = failure.CategoryTimeout, true
	case ErrorExecutionCanceled:
		info.Category = failure.CategoryCanceled
	case ErrorBudgetExceeded:
		info.Category = failure.CategoryCapacity
	case ErrorIdempotencyRequired:
		info.Category = failure.CategoryValidation
	case ErrorEffectReconciliation, ErrorEffectJournal:
		info.Category = failure.CategoryAvailability
	case ErrorArtifactUnavailable:
		info.Category = failure.CategoryAvailability
	case ErrorSecurityPolicyDenied, ErrorSecurityScopeInvalid, ErrorCredentialScope, ErrorApprovalRequired:
		info.Category = failure.CategoryValidation
	case ErrorSecurityAudit:
		info.Category = failure.CategoryAvailability
	}
	return info
}
