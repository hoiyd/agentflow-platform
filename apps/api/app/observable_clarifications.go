package app

import (
	"agentflow-platform/apps/api/internal/clarification"
	"agentflow-platform/apps/api/internal/domain"
	"context"
	"encoding/json"
)

// All committed sequence slots must reach the Hub, including atomic Store
// operations that do not pass through CreateRunEvent. No publication on rollback.
type observableClarifications struct {
	clarification.Store
	publish func(domain.RunEvent)
}

func (s observableClarifications) observe(m clarification.Mutation, err error) (clarification.Mutation, error) {
	if err == nil {
		for _, event := range m.Events {
			s.publish(event)
		}
	}
	return m, err
}
func (s observableClarifications) SuspendClarification(ctx context.Context, c domain.Clarification, data json.RawMessage) (clarification.Mutation, error) {
	return s.observe(s.Store.SuspendClarification(ctx, c, data))
}
func (s observableClarifications) AnswerClarification(ctx context.Context, owner, workspace, run, id string, input domain.ClarificationAnswer) (clarification.Mutation, error) {
	return s.observe(s.Store.AnswerClarification(ctx, owner, workspace, run, id, input))
}
func (s observableClarifications) ClaimClarification(ctx context.Context, owner, workspace, run, id string) (clarification.Mutation, error) {
	return s.observe(s.Store.ClaimClarification(ctx, owner, workspace, run, id))
}
func (s observableClarifications) WithdrawClarification(ctx context.Context, owner, workspace, run, id string, version int64) (clarification.Mutation, error) {
	return s.observe(s.Store.WithdrawClarification(ctx, owner, workspace, run, id, version))
}
