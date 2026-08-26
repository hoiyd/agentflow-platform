package app

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/event"
	"agentflow-platform/apps/api/internal/store"
)

// observableStore publishes events only after the underlying commit succeeds.
// Embedding preserves every non-event Store capability without another proxy
// layer per persistence method.
type observableStore struct {
	store.Store
	hub *event.Hub
}

func newObservableStore(backend store.Store, hub *event.Hub) *observableStore {
	return &observableStore{Store: backend, hub: hub}
}

func (s *observableStore) CreateRunEvent(item domain.RunEvent) (domain.RunEvent, error) {
	created, err := s.Store.CreateRunEvent(item)
	if err == nil {
		s.hub.PublishCommitted(created)
	}
	return created, err
}

func (s *observableStore) ForWorkspace(scope domain.WorkspaceScope) store.WorkspaceStore {
	return observableWorkspaceStore{WorkspaceStore: s.Store.ForWorkspace(scope), hub: s.hub}
}

func (s *observableStore) Close() error {
	if closer, ok := s.Store.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}

type observableWorkspaceStore struct {
	store.WorkspaceStore
	hub *event.Hub
}

func (s observableWorkspaceStore) CreateRunEvent(item domain.RunEvent) (domain.RunEvent, error) {
	created, err := s.WorkspaceStore.CreateRunEvent(item)
	if err == nil {
		s.hub.PublishCommitted(created)
	}
	return created, err
}
