package reconciliation

import (
	"context"
	"errors"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
)

func authorizedReconciliationBinding(catalog *tool.Catalog, record domain.ToolEffectRecord, action domain.ToolEffectReconciliationAction) (tool.Binding, error) {
	binding, err := reconciliationBinding(catalog, record)
	if err != nil {
		return tool.Binding{}, err
	}
	if binding.UnavailableReason != "" {
		return tool.Binding{}, &ReconciliationError{Code: ReconciliationUnavailable, Message: "reconciliation prerequisites are unavailable: " + binding.UnavailableReason}
	}
	if (action == domain.ToolEffectRetrySameKey && binding.Reconciliation.RetryWithSameKey == nil) ||
		(action == domain.ToolEffectVerifyCompensation && binding.Reconciliation.VerifyCompensation == nil) ||
		(action == domain.ToolEffectCompensate && (binding.Reconciliation.Compensate == nil || binding.Descriptor.Security.Reversibility == policy.Irreversible)) {
		return tool.Binding{}, &ReconciliationError{Code: ReconciliationUnavailable, Message: "tool does not support this reconciliation action"}
	}
	// Recovery callbacks have no original arguments. Authorize their full declared
	// scope with the current operator policy, never a model-supplied scope/actor.
	// No credential resolver exists here: credential-requiring callbacks fail closed.
	decision := policy.Evaluate(catalog.SecurityPolicy(), policy.Request{
		Tool: record.ToolName, Declared: binding.Descriptor.Security,
		RequestedScope: binding.Descriptor.Security.Scope,
	})
	// These are authenticated, explicit recovery commands over an existing
	// receipt, not model Tool calls. Phase A ask does not add a second approval
	// to Undo/verification; deny, human_only and scope mismatches stay blocked.
	if !decision.Allowed && !policy.CanRequestApproval(record.ToolName, decision) {
		return tool.Binding{}, &ReconciliationError{Code: ReconciliationUnavailable, Message: "reconciliation denied by tool policy: " + decision.Reason}
	}
	return binding, nil
}

func boundedReconciliationAction(ctx context.Context, catalog *tool.Catalog, record domain.ToolEffectRecord, command ToolEffectReconciliationCommand) (domain.ToolEffectStatus, []byte, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if command.Action != domain.ToolEffectRetrySameKey && command.Action != domain.ToolEffectCompensate && command.Action != domain.ToolEffectVerifyCompensation {
		return executeReconciliationAction(ctx, catalog, record, command)
	}
	type outcome struct {
		status domain.ToolEffectStatus
		result []byte
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		defer func() {
			if recover() != nil {
				// Includes user-defined JSON marshalers, not only the callback body.
				done <- outcome{err: errors.New("reconciliation callback or result encoding panicked")}
			}
		}()
		if err := ctx.Err(); err != nil {
			done <- outcome{err: err}
			return
		}
		status, result, err := executeReconciliationAction(ctx, catalog, record, command)
		done <- outcome{status, result, err}
	}()
	select {
	case <-ctx.Done():
		return "", nil, ctx.Err()
	case result := <-done:
		// A late success cannot certify an outcome after the deadline.
		if err := ctx.Err(); err != nil {
			return "", nil, err
		}
		return result.status, result.result, result.err
	}
}
