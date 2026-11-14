package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"agentflow-platform/apps/api/internal/agent/toolloop"
	"agentflow-platform/apps/api/internal/agent/turn"
	"agentflow-platform/apps/api/internal/approval"
	"agentflow-platform/apps/api/internal/checkpoint"
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/availability"
	"agentflow-platform/apps/api/internal/tool/policy"
)

type toolApprovalCheckpoint struct {
	Version      int
	Request      turn.Request
	Continuation toolloop.Continuation
}

func (r *Runtime) toolApprovalBoundary(request turn.Request) func(context.Context, tool.ExecutionRequest, toolloop.Continuation) error {
	if r.toolApprovals == nil || request.Role != "" && request.Role != "act" && request.Role != "worker" {
		return nil
	}
	return func(ctx context.Context, call tool.ExecutionRequest, c toolloop.Continuation) error {
		run, ok, err := r.store.GetRun(request.RunID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrRuntimeSnapshotUnavailable
		}
		hash, _, err := checkpoint.RuntimeHashes(run.RuntimeSnapshot)
		if err != nil {
			return err
		}
		record := domain.ToolApproval{RunID: run.ID, ConversationID: run.ConversationID, StageID: request.StepID, TurnID: request.TurnID, ToolCallID: call.CallID, Tool: call.Tool, Arguments: call.Arguments, ArgumentsHash: call.ArgumentsHash, DefinitionRevision: call.DefinitionRevision, SnapshotHash: hash, PolicyHash: approval.Hash(policy.NormalizePolicy(run.RuntimeSnapshot.ToolSecurityPolicy))}
		if err = r.validateToolApproval(ctx, run, record, request.Agent, call); err != nil {
			return err
		}
		request.Catalog, request.Sink, request.Continuation = nil, nil, nil
		data, err := json.Marshal(toolApprovalCheckpoint{Version: 1, Request: request, Continuation: c})
		if err != nil {
			return err
		}
		_, err = r.toolApprovals.SuspendToolApproval(ctx, record, data)
		return err
	}
}

func (r *Runtime) validateToolApproval(ctx context.Context, run domain.Run, record domain.ToolApproval, agent domain.Agent, call tool.ExecutionRequest) error {
	hash, _, err := checkpoint.RuntimeHashes(run.RuntimeSnapshot)
	if err != nil || hash != record.SnapshotHash || call.RunID != record.RunID || call.StageID != record.StageID || call.TurnID != record.TurnID || call.CallID != record.ToolCallID || call.Tool != record.Tool {
		return approval.ErrConflict
	}
	current, err := r.currentCatalog(run.WorkspaceID)
	if err != nil {
		return err
	}
	current, _, err = availability.Resolve(r.store, run.WorkspaceID, current)
	if err != nil {
		return err
	}
	if approval.Hash(policy.NormalizePolicy(current.SecurityPolicy())) != record.PolicyHash {
		return fmt.Errorf("%w: Tool policy changed", approval.ErrConflict)
	}
	validated, invalid := current.ValidateCall(call.Tool, call.Arguments)
	if invalid != nil {
		return invalid
	}
	if validated.DefinitionRevision != record.DefinitionRevision || validated.ArgumentsHash != record.ArgumentsHash {
		return fmt.Errorf("%w: Tool definition or arguments changed", approval.ErrConflict)
	}
	canonical, invalid := current.ValidateCall(record.Tool, record.Arguments)
	if invalid != nil || canonical.ArgumentsHash != validated.ArgumentsHash {
		return approval.ErrConflict
	}
	binding, ok := current.ResolveReady(call.Tool)
	if !ok {
		return approval.ErrConflict
	}
	decision := policy.Evaluate(current.SecurityPolicy(), policy.Request{Tool: call.Tool, Declared: binding.Descriptor.Security, RequestedScope: binding.Descriptor.Security.Scope})
	if !policy.CanRequestApproval(call.Tool, decision) {
		return approval.ErrConflict
	}
	return r.authorizeTool(run.ID, agent)(ctx, call, binding.Descriptor.Security.Scope)
}
