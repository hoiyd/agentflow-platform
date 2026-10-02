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

func (s *observableStore) CommitToolEffectReconciliation(mutation domain.ToolEffectReconciliation) (domain.ToolEffectRecord, domain.RunEvent, bool, error) {
	effect, committed, applied, err := s.Store.CommitToolEffectReconciliation(mutation)
	if err == nil && applied {
		s.hub.PublishCommitted(committed)
	}
	return effect, committed, applied, err
}

func (s *observableStore) ForWorkspace(scope domain.WorkspaceScope) store.WorkspaceStore {
	// Scope the observable backend itself: optional read capabilities (Artifacts)
	// remain available, and event writes still publish only after commit.
	return store.ScopeWorkspace(s, scope)
}

func (s *observableStore) Close() error {
	if closer, ok := s.Store.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}
