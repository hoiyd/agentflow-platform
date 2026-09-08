package fixturestore

import (
	"bytes"
	"errors"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func (s *Store) CreateToolArtifact(artifact domain.ToolArtifact, content []byte) (domain.ToolArtifact, error) {
	if err := store.ValidateToolArtifact(artifact, content); err != nil {
		return domain.ToolArtifact{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasRunLocked(artifact.RunID) {
		return domain.ToolArtifact{}, store.ErrNotFound("run")
	}
	for _, item := range s.data.ToolArtifacts {
		if item.ID == artifact.ID {
			if item.RunID != artifact.RunID || item.ContentHash != artifact.ContentHash || item.ToolCallID != artifact.ToolCallID {
				return domain.ToolArtifact{}, errors.New("tool artifact idempotency conflict")
			}
			if store.ToolArtifactExpired(item, time.Now().UTC()) {
				return domain.ToolArtifact{}, store.ErrToolArtifactExpired
			}
			return item, nil
		}
	}
	s.data.ToolArtifacts = append(s.data.ToolArtifacts, artifact)
	s.artifactContent[artifact.ID] = bytes.Clone(content)
	return artifact, nil
}

func (s *Store) ListToolArtifacts(runID string) ([]domain.ToolArtifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.hasRunLocked(runID) {
		return nil, store.ErrNotFound("run")
	}
	return store.ToolArtifactsForRun(s.data.ToolArtifacts, runID), nil
}

func (s *Store) artifactLocked(runID, id string) (domain.ToolArtifact, []byte, error) {
	for _, item := range s.data.ToolArtifacts {
		if item.RunID == runID && item.ID == id {
			if store.ToolArtifactExpired(item, time.Now().UTC()) {
				return domain.ToolArtifact{}, nil, store.ErrToolArtifactExpired
			}
			return item, s.artifactContent[id], nil
		}
	}
	return domain.ToolArtifact{}, nil, store.ErrNotFound("tool artifact")
}

func (s *Store) ReadToolArtifact(runID, id string, offset, limit int) (domain.ToolArtifactRead, error) {
	offset, limit, err := store.NormalizeArtifactRead(offset, limit)
	if err != nil {
		return domain.ToolArtifactRead{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, content, err := s.artifactLocked(runID, id)
	if err != nil {
		return domain.ToolArtifactRead{}, err
	}
	if offset > len(content) {
		return domain.ToolArtifactRead{}, store.ErrToolArtifactRange
	}
	end := min(len(content), offset+limit)
	return domain.ToolArtifactRead{Artifact: item, Offset: offset, Content: string(content[offset:end]), NextOffset: end, Complete: end == len(content)}, nil
}

func (s *Store) SearchToolArtifact(runID, id, query string, maxMatches int) (domain.ToolArtifactSearchResult, error) {
	query, maxMatches, err := store.NormalizeArtifactSearch(query, maxMatches)
	if err != nil {
		return domain.ToolArtifactSearchResult{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, content, err := s.artifactLocked(runID, id)
	if err != nil {
		return domain.ToolArtifactSearchResult{}, err
	}
	return store.SearchToolArtifact(item, content, query, maxMatches), nil
}
