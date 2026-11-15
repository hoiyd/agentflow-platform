package app

import (
	"context"
	"encoding/json"

	"agentflow-platform/apps/api/internal/approval"
	"agentflow-platform/apps/api/internal/domain"
)

type observableToolApprovals struct {
	approval.Store
	publish func(domain.RunEvent)
}

func (s observableToolApprovals) observe(m approval.Mutation, err error) (approval.Mutation, error) {
	if err == nil {
		for _, item := range m.Events {
			s.publish(item)
		}
	}
	return m, err
}
func (s observableToolApprovals) SuspendToolApproval(ctx context.Context, item domain.ToolApproval, data json.RawMessage) (approval.Mutation, error) {
	return s.observe(s.Store.SuspendToolApproval(ctx, item, data))
}
func (s observableToolApprovals) DecideToolApproval(ctx context.Context, owner, workspace, run, id string, input domain.ToolApprovalDecision) (approval.Mutation, error) {
	return s.observe(s.Store.DecideToolApproval(ctx, owner, workspace, run, id, input))
}
func (s observableToolApprovals) ClaimToolApproval(ctx context.Context, owner, workspace, run, id string) (approval.Mutation, error) {
	return s.observe(s.Store.ClaimToolApproval(ctx, owner, workspace, run, id))
}
func (s observableToolApprovals) ConsumeToolApproval(ctx context.Context, owner, workspace, id, claim, bindingHash string) (approval.Mutation, error) {
	return s.observe(s.Store.ConsumeToolApproval(ctx, owner, workspace, id, claim, bindingHash))
}
