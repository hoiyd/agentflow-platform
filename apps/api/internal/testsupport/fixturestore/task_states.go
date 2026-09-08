package fixturestore

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
	"errors"
	"sort"
	"strings"
	"time"
)

func (s *Store) GetTaskState(conversationID string) (domain.TaskState, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.getConversationLocked(conversationID); !ok {
		return domain.TaskState{}, false, store.ErrNotFound("conversation")
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
	return store.CloneTaskState(latest.State), true, nil
}

func (s *Store) GetTaskStateRevision(conversationID string, version int64) (domain.TaskStateRevision, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.getConversationLocked(conversationID); !ok {
		return domain.TaskStateRevision{}, false, store.ErrNotFound("conversation")
	}
	for _, item := range s.data.TaskStateRevisions {
		if item.ConversationID == conversationID && item.Version == version {
			return store.CloneTaskStateRevision(item), true, nil
		}
	}
	return domain.TaskStateRevision{}, false, nil
}

func (s *Store) ListTaskStateRevisions(conversationID string) ([]domain.TaskStateRevision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.getConversationLocked(conversationID); !ok {
		return nil, store.ErrNotFound("conversation")
	}
	items := make([]domain.TaskStateRevision, 0)
	for _, item := range s.data.TaskStateRevisions {
		if item.ConversationID == conversationID {
			items = append(items, store.CloneTaskStateRevision(item))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Version < items[j].Version })
	return items, nil
}

func (s *Store) ApplyTaskStatePatch(conversationID string, patch domain.TaskStatePatch, source domain.TaskStateSource) (domain.TaskStateRevision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	conversation, ok := s.getConversationLocked(conversationID)
	if !ok {
		return domain.TaskStateRevision{}, store.ErrNotFound("conversation")
	}
	if err := s.validateTaskStateSourceLocked(conversationID, source); err != nil {
		return domain.TaskStateRevision{}, store.ClassifyTaskStateValidation(err)
	}
	current := domain.EmptyTaskState(conversation.WorkspaceID, conversationID)
	for _, item := range s.data.TaskStateRevisions {
		if item.ConversationID == conversationID && item.Version > current.Version {
			current = store.CloneTaskState(item.State)
		}
	}
	if patch.ExpectedVersion != current.Version {
		return domain.TaskStateRevision{}, &store.TaskStateVersionConflict{Expected: patch.ExpectedVersion, Actual: current.Version}
	}
	now := time.Now().UTC()
	next, err := domain.ApplyTaskStatePatch(current, patch, now)
	if err != nil {
		return domain.TaskStateRevision{}, store.ClassifyTaskStateValidation(err)
	}
	revision := domain.TaskStateRevision{
		ID: store.NewID("tsr"), WorkspaceID: conversation.WorkspaceID, ConversationID: conversationID,
		Version: next.Version, PreviousVersion: current.Version, Patch: patch, State: next,
		Source: store.NormalizeTaskStateSource(source), CreatedAt: now,
	}
	s.data.TaskStateRevisions = append(s.data.TaskStateRevisions, store.CloneTaskStateRevision(revision))

	return store.CloneTaskStateRevision(revision), nil
}

func (s *Store) validateTaskStateSourceLocked(conversationID string, source domain.TaskStateSource) error {
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
