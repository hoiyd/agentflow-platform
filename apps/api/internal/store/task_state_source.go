package store

import (
	"strings"

	"agentflow-platform/apps/api/internal/domain"
)

func NormalizeTaskStateSource(source domain.TaskStateSource) domain.TaskStateSource {
	source.ActorType = strings.TrimSpace(source.ActorType)
	if source.ActorType == "" {
		source.ActorType = "system"
	}
	source.ActorID = strings.TrimSpace(source.ActorID)
	source.RunID = strings.TrimSpace(source.RunID)
	source.StageID = strings.TrimSpace(source.StageID)
	source.TurnID = strings.TrimSpace(source.TurnID)
	source.SourceMessageID = strings.TrimSpace(source.SourceMessageID)
	return source
}
