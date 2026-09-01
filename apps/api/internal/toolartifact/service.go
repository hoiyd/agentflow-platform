package toolartifact

import (
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/store"
)

type Service struct {
	store  store.ToolArtifactStore
	tracer *event.Recorder
}

func NewService(artifactStore store.ToolArtifactStore, tracer *event.Recorder) *Service {
	if artifactStore == nil {
		return nil
	}
	return &Service{store: artifactStore, tracer: tracer}
}
