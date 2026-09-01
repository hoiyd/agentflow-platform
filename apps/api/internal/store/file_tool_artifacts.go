package store

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func (s *FileStore) CreateToolArtifact(artifact domain.ToolArtifact, content []byte) (domain.ToolArtifact, error) {
	if err := validateToolArtifact(artifact, content); err != nil {
		return domain.ToolArtifact{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasRunLocked(artifact.RunID) {
		return domain.ToolArtifact{}, ErrNotFound("run")
	}
	for _, existing := range s.data.ToolArtifacts {
		if existing.ID != artifact.ID {
			continue
		}
		if existing.RunID != artifact.RunID || existing.ContentHash != artifact.ContentHash || existing.ToolCallID != artifact.ToolCallID {
			return domain.ToolArtifact{}, errors.New("tool artifact idempotency conflict")
		}
		existingContent, err := s.readCompleteToolArtifact(existing)
		if err != nil || len(existingContent) != existing.StoredByteSize {
			return domain.ToolArtifact{}, errors.New("existing tool artifact content is unavailable or corrupt")
		}
		return existing, nil
	}
	path, err := s.toolArtifactPath(artifact.ID)
	if err != nil {
		return domain.ToolArtifact{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return domain.ToolArtifact{}, err
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return domain.ToolArtifact{}, err
	}
	handle, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return domain.ToolArtifact{}, fmt.Errorf("create tool artifact content: %w", err)
	}
	removeContent := true
	defer func() {
		_ = handle.Close()
		if removeContent {
			_ = os.Remove(path)
		}
	}()
	if _, err := handle.Write(content); err != nil {
		return domain.ToolArtifact{}, err
	}
	if err := handle.Sync(); err != nil {
		return domain.ToolArtifact{}, err
	}
	if err := handle.Close(); err != nil {
		return domain.ToolArtifact{}, err
	}
	s.data.ToolArtifacts = append(s.data.ToolArtifacts, artifact)
	if err := s.saveLocked(); err != nil {
		s.data.ToolArtifacts = s.data.ToolArtifacts[:len(s.data.ToolArtifacts)-1]
		return domain.ToolArtifact{}, err
	}
	removeContent = false
	return artifact, nil
}

func (s *FileStore) ListToolArtifacts(runID string) ([]domain.ToolArtifact, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.hasRunLocked(runID) {
		return nil, ErrNotFound("run")
	}
	artifacts := make([]domain.ToolArtifact, 0)
	now := time.Now().UTC()
	for _, artifact := range s.data.ToolArtifacts {
		if artifact.RunID == runID {
			artifact.Expired = toolArtifactExpired(artifact, now)
			artifacts = append(artifacts, artifact)
		}
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].CreatedAt.Before(artifacts[j].CreatedAt) })
	return artifacts, nil
}

func (s *FileStore) ReadToolArtifact(runID string, artifactID string, offset int, limit int) (domain.ToolArtifactRead, error) {
	offset, limit, err := normalizeArtifactRead(offset, limit)
	if err != nil {
		return domain.ToolArtifactRead{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	artifact, ok := s.toolArtifactLocked(runID, artifactID)
	if !ok {
		return domain.ToolArtifactRead{}, ErrNotFound("tool artifact")
	}
	if toolArtifactExpired(artifact, time.Now().UTC()) {
		return domain.ToolArtifactRead{}, ErrToolArtifactExpired
	}
	if offset > artifact.StoredByteSize {
		return domain.ToolArtifactRead{}, ErrToolArtifactRange
	}
	path, err := s.toolArtifactPath(artifact.ID)
	if err != nil {
		return domain.ToolArtifactRead{}, err
	}
	handle, err := os.Open(path)
	if err != nil {
		return domain.ToolArtifactRead{}, err
	}
	defer handle.Close()
	if _, err := handle.Seek(int64(offset), io.SeekStart); err != nil {
		return domain.ToolArtifactRead{}, err
	}
	buffer := make([]byte, min(limit, artifact.StoredByteSize-offset))
	read, err := io.ReadFull(handle, buffer)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return domain.ToolArtifactRead{}, err
	}
	buffer = buffer[:read]
	return domain.ToolArtifactRead{
		Artifact: artifact, Offset: offset, Content: string(buffer),
		NextOffset: offset + read, Complete: offset+read >= artifact.StoredByteSize,
	}, nil
}

func (s *FileStore) SearchToolArtifact(runID string, artifactID string, query string, maxMatches int) (domain.ToolArtifactSearchResult, error) {
	query, maxMatches, err := normalizeArtifactSearch(query, maxMatches)
	if err != nil {
		return domain.ToolArtifactSearchResult{}, err
	}
	artifact, ok := s.toolArtifactMetadata(runID, artifactID)
	if !ok {
		return domain.ToolArtifactSearchResult{}, ErrNotFound("tool artifact")
	}
	content, err := s.readCompleteToolArtifact(artifact)
	if err != nil {
		return domain.ToolArtifactSearchResult{}, err
	}
	return searchToolArtifact(artifact, content, query, maxMatches), nil
}

func (s *FileStore) toolArtifactLocked(runID string, artifactID string) (domain.ToolArtifact, bool) {
	for _, artifact := range s.data.ToolArtifacts {
		if artifact.ID == artifactID && artifact.RunID == runID {
			return artifact, true
		}
	}
	return domain.ToolArtifact{}, false
}

func (s *FileStore) toolArtifactMetadata(runID string, artifactID string) (domain.ToolArtifact, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.toolArtifactLocked(runID, artifactID)
}

func (s *FileStore) readCompleteToolArtifact(artifact domain.ToolArtifact) ([]byte, error) {
	if toolArtifactExpired(artifact, time.Now().UTC()) {
		return nil, ErrToolArtifactExpired
	}
	path, err := s.toolArtifactPath(artifact.ID)
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(content) != artifact.StoredByteSize || toolArtifactContentHash(content) != artifact.ContentHash {
		return nil, errors.New("tool artifact content integrity check failed")
	}
	return content, nil
}

func (s *FileStore) toolArtifactPath(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" || filepath.Base(id) != id || !strings.HasPrefix(id, "tool_artifact_") {
		return "", errors.New("invalid tool artifact id")
	}
	return filepath.Join(s.path+".tool-artifacts", id+".bin"), nil
}

func (s *FileStore) purgeExpiredToolArtifactContent(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, artifact := range s.data.ToolArtifacts {
		if !toolArtifactExpired(artifact, now) {
			continue
		}
		path, err := s.toolArtifactPath(artifact.ID)
		if err != nil {
			return err
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
