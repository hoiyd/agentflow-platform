package store

import (
	"errors"
	"sort"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func (s *FileStore) GetTaskState(conversationID string) (domain.TaskState, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.getConversationLocked(conversationID); !ok {
		return domain.TaskState{}, false, ErrNotFound("conversation")
	}
	var latest *domain.TaskStateRevision
	for index := range s.data.TaskStateRevisions {
		item := &s.data.TaskStateRevisions[index]
		if item.ConversationID == conversationID && (latest == nil || item.Version > latest.Version) {
			latest = item
		}
	}
	if latest == nil {
		return domain.TaskState{}, false, nil
	}
	return cloneTaskState(latest.State), true, nil
}

func (s *FileStore) GetTaskStateRevision(conversationID string, version int64) (domain.TaskStateRevision, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.getConversationLocked(conversationID); !ok {
		return domain.TaskStateRevision{}, false, ErrNotFound("conversation")
	}
	for _, item := range s.data.TaskStateRevisions {
		if item.ConversationID == conversationID && item.Version == version {
			return cloneTaskStateRevision(item), true, nil
		}
	}
	return domain.TaskStateRevision{}, false, nil
}

func (s *FileStore) ListTaskStateRevisions(conversationID string) ([]domain.TaskStateRevision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.getConversationLocked(conversationID); !ok {
		return nil, ErrNotFound("conversation")
	}
	items := make([]domain.TaskStateRevision, 0)
	for _, item := range s.data.TaskStateRevisions {
		if item.ConversationID == conversationID {
			items = append(items, cloneTaskStateRevision(item))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Version < items[j].Version })
	return items, nil
}

func (s *FileStore) ApplyTaskStatePatch(conversationID string, patch domain.TaskStatePatch, source domain.TaskStateSource) (domain.TaskStateRevision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	conversation, ok := s.getConversationLocked(conversationID)
	if !ok {
		return domain.TaskStateRevision{}, ErrNotFound("conversation")
	}
	if err := s.validateTaskStateSourceLocked(conversationID, source); err != nil {
		return domain.TaskStateRevision{}, classifyTaskStateValidation(err)
	}
	current := domain.EmptyTaskState(conversation.WorkspaceID, conversationID)
	for _, item := range s.data.TaskStateRevisions {
		if item.ConversationID == conversationID && item.Version > current.Version {
			current = cloneTaskState(item.State)
		}
	}
	if patch.ExpectedVersion != current.Version {
		return domain.TaskStateRevision{}, &TaskStateVersionConflict{Expected: patch.ExpectedVersion, Actual: current.Version}
	}
	now := time.Now().UTC()
	next, err := domain.ApplyTaskStatePatch(current, patch, now)
	if err != nil {
		return domain.TaskStateRevision{}, classifyTaskStateValidation(err)
	}
	revision := domain.TaskStateRevision{
		ID: newID("tsr"), WorkspaceID: conversation.WorkspaceID, ConversationID: conversationID,
		Version: next.Version, PreviousVersion: current.Version, Patch: patch, State: next,
		Source: normalizeTaskStateSource(source), CreatedAt: now,
	}
	s.data.TaskStateRevisions = append(s.data.TaskStateRevisions, cloneTaskStateRevision(revision))
	if err := s.saveLocked(); err != nil {
		s.data.TaskStateRevisions = s.data.TaskStateRevisions[:len(s.data.TaskStateRevisions)-1]
		return domain.TaskStateRevision{}, err
	}
	return cloneTaskStateRevision(revision), nil
}

func (s *FileStore) validateTaskStateSourceLocked(conversationID string, source domain.TaskStateSource) error {
	if runID := strings.TrimSpace(source.RunID); runID != "" {
		run, ok := s.getRunLocked(runID)
		if !ok || run.ConversationID != conversationID {
			return errors.New("task state source run does not belong to conversation")
		}
	}
	if messageID := strings.TrimSpace(source.SourceMessageID); messageID != "" {
		for _, message := range s.data.Messages {
			if message.ID == messageID && message.ConversationID == conversationID {
				return nil
			}
		}
		return errors.New("task state source message does not belong to conversation")
	}
	return nil
}

func normalizeTaskStateSource(source domain.TaskStateSource) domain.TaskStateSource {
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
