package agent

import (
	"context"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/rag/eligibility"
)

type knowledgeScopeKey struct{}

// New admitted Runs receive a validated detached scope. Resume ignores draft
// settings and reads the persisted Snapshot instead.
func WithKnowledgeScope(ctx context.Context, scope *domain.KnowledgeScope) context.Context {
	return context.WithValue(ctx, knowledgeScopeKey{}, scope)
}

func freezeKnowledgeScope(ctx context.Context, snapshot *domain.RuntimeSnapshot) error {
	scope, _ := ctx.Value(knowledgeScopeKey{}).(*domain.KnowledgeScope)
	if scope == nil {
		return nil
	}
	filter, err := eligibility.FrozenFilter(&domain.RuntimeSnapshot{KnowledgeScope: scope})
	if err != nil {
		return err
	}
	snapshot.KnowledgeScope, err = eligibility.FreezeScope(filter)
	return err
}
