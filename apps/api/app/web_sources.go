package app

import (
	"context"
	"errors"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/tool"
	"agentflow-platform/apps/api/internal/tool/webcitation"
)

type webSourceStore interface {
	GetRun(string) (domain.Run, bool, error)
	ListRunEvents(string) ([]domain.RunEvent, error)
}

func runWebSourceResolver(storage webSourceStore) tool.WebSourceResolver {
	return func(ctx context.Context, sourceID string) (domain.WebCitation, error) {
		if err := ctx.Err(); err != nil {
			return domain.WebCitation{}, err
		}
		scope := event.ScopeFromContext(ctx)
		if scope.RunID == "" || scope.ConversationID == "" {
			return domain.WebCitation{}, errors.New("web source requires Run and Conversation scope")
		}
		run, ok, err := storage.GetRun(scope.RunID)
		if err != nil {
			return domain.WebCitation{}, err
		}
		if !ok || run.ConversationID != scope.ConversationID || run.WorkspaceID == "" {
			return domain.WebCitation{}, errors.New("web source scope does not match persisted Run")
		}
		events, err := storage.ListRunEvents(run.ID)
		if err != nil {
			return domain.WebCitation{}, err
		}
		source, found := webcitation.FromEvents(events).Lookup(sourceID)
		if !found || source.RunID != run.ID {
			return domain.WebCitation{}, errors.New("web source is not in this Run")
		}
		return source, nil
	}
}
