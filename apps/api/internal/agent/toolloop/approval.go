package toolloop

import (
	"context"
	"fmt"

	"agentflow-platform/apps/api/internal/approval"
	"agentflow-platform/apps/api/internal/failure"
	"agentflow-platform/apps/api/internal/inference/provider"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/policy"
)

func validateApprovalBatch(calls []provider.ToolCall, catalog *tool.Catalog) error {
	if len(calls) < 2 {
		return nil
	}
	config := catalog.SecurityPolicy()
	for _, call := range calls {
		action := config.DefaultAction
		for _, rule := range config.Rules {
			if rule.Tool == call.Function.Name {
				action = rule.Action
				break
			}
		}
		// This preflight only prevents partial batch effects; the Executor still
		// resolves arguments, scope and credentials before admitting an approval.
		if policy.IsApprovalRequired(action) {
			return failure.New(failure.Definition{Message: fmt.Sprintf("%s requiring approval must be called alone; no part of the batch was executed", call.Function.Name), Info: failure.Info{Code: "tool_approval_batch_unsupported", Source: "tool_approval", Category: failure.CategoryValidation}})
		}
	}
	return nil
}

func requiresApproval(result tool.ExecutionResult) bool {
	return result.Error != nil && result.Error.Code == tool.ErrorApprovalRequired && result.PolicyDecision != nil && policy.CanRequestApproval(*result.PolicyDecision)
}

func suspendApproval(ctx context.Context, request Request, pending tool.ExecutionRequest, result tool.ExecutionResult, c Continuation) error {
	pending.Arguments, pending.ArgumentsHash, pending.DefinitionRevision = result.Arguments, result.ArgumentsHash, result.DefinitionRevision
	c.PendingCallID, c.Approval = pending.CallID, &pending
	for _, message := range c.Messages {
		c.Sources = append(c.Sources, message.Source)
		c.References = append(c.References, message.ReferenceID)
	}
	if err := request.SuspendApproval(ctx, pending, c); err != nil {
		return err
	}
	return approval.Pending
}
