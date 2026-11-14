package toolloop

import (
	"context"

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
	for _, rule := range catalog.SecurityPolicy().Rules {
		if rule.Tool != "file_patch" || rule.Action != policy.ActionAsk {
			continue
		}
		for _, call := range calls {
			if call.Function.Name == rule.Tool {
				return failure.New(failure.Definition{Message: "file_patch requiring approval must be called alone; no part of the batch was executed", Info: failure.Info{Code: "tool_approval_batch_unsupported", Source: "tool_approval", Category: failure.CategoryValidation}})
			}
		}
	}
	return nil
}

func requiresApproval(result tool.ExecutionResult) bool {
	return result.Error != nil && result.Error.Code == tool.ErrorApprovalRequired && result.PolicyDecision != nil && policy.CanRequestApproval(result.Tool, *result.PolicyDecision)
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
