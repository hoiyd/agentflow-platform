package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"agentflow-platform/apps/api/internal/tool/policy"
)

func (e *Executor) evaluateSecurity(ctx context.Context, request ExecutionRequest, binding Binding) (policy.Decision, *ExecutionError) {
	requestedScope := binding.Descriptor.Security.Scope
	if binding.ResolveScope != nil {
		resolved, err := resolveToolScope(ctx, binding.ResolveScope, request.Arguments)
		if err != nil {
			decision := policy.Decision{
				Action: policy.ActionDeny, PolicyVersion: e.securityPolicy.Version,
				Reason: "scope_resolution_failed", Capability: binding.Descriptor.Security,
			}
			return decision, executionError(ErrorSecurityScopeInvalid, "Tool security scope could not be resolved", err)
		}
		requestedScope = resolved
	}
	decision := policy.Evaluate(e.securityPolicy, policy.Request{
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

func resolveToolScope(ctx context.Context, resolver ScopeResolver, arguments json.RawMessage) (scope policy.Scope, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("Tool scope resolver panicked: %v", recovered)
		}
	}()
	return resolver(ctx, arguments)
}

func (e *Executor) tracePolicyDecision(ctx context.Context, request ExecutionRequest, decision policy.Decision) error {
	tracer, ok := e.tracer.(PolicyDecisionTracer)
	if !ok {
		if decision.AuditRequired() {
			return errors.New("Tool policy decision tracer is unavailable")
		}
		return nil
	}
	return tracer.ToolPolicyEvaluated(ctx, request, decision)
}
