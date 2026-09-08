package fixturestore

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
	"errors"

	"sort"
	"time"
)

func (s *Store) ListConversations() ([]domain.Conversation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.data.Conversations) == 0 {
		return []domain.Conversation{}, nil
	}
	items := append([]domain.Conversation(nil), s.data.Conversations...)
	sort.Slice(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	return items, nil
}

func (s *Store) CreateConversation(title string) (domain.Conversation, error) {
	return s.CreateConversationInWorkspace(domain.DefaultWorkspaceID, title)
}

func (s *Store) CreateConversationInWorkspace(workspaceID string, title string) (domain.Conversation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	conversation := domain.Conversation{
		ID:          store.NewID("conv"),
		WorkspaceID: store.NormalizeWorkspaceID(workspaceID),
		Title:       store.NormalizeTitle(title),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	s.data.Conversations = append(s.data.Conversations, conversation)
	return conversation, nil
}

func (s *Store) ListConversationsByWorkspace(workspaceID string) ([]domain.Conversation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	workspaceID = store.NormalizeWorkspaceID(workspaceID)
	items := []domain.Conversation{}
	for _, item := range s.data.Conversations {
		if item.WorkspaceID == workspaceID {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	return items, nil
}

func (s *Store) GetConversationInWorkspace(workspaceID string, id string) (domain.Conversation, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	workspaceID = store.NormalizeWorkspaceID(workspaceID)
	for _, item := range s.data.Conversations {
		if item.ID == id && item.WorkspaceID == workspaceID {
			return item, true, nil
		}
	}
	return domain.Conversation{}, false, nil
}

func (s *Store) DeleteConversationInWorkspace(workspaceID string, id string) error {
	if _, ok, err := s.GetConversationInWorkspace(workspaceID, id); err != nil {
		return err
	} else if !ok {
		return store.ErrNotFound("conversation")
	}
	return s.DeleteConversation(id)
}

func (s *Store) GetConversation(id string) (domain.Conversation, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, item := range s.data.Conversations {
		if item.ID == id {
			return item, true, nil
		}
	}
	return domain.Conversation{}, false, nil
}

func (s *Store) DeleteConversation(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.hasConversationLocked(id) {
		return store.ErrNotFound("conversation")
	}

	conversations := make([]domain.Conversation, 0, len(s.data.Conversations))
	for _, conversation := range s.data.Conversations {
		if conversation.ID != id {
			conversations = append(conversations, conversation)
		}
	}
	s.data.Conversations = conversations

	messages := make([]domain.Message, 0, len(s.data.Messages))
	for _, message := range s.data.Messages {
		if message.ConversationID != id {
			messages = append(messages, message)
		}
	}
	s.data.Messages = messages

	runIDs := map[string]bool{}
	runs := make([]domain.Run, 0, len(s.data.Runs))
	for _, run := range s.data.Runs {
		if run.ConversationID == id {
			runIDs[run.ID] = true
			continue
		}
		runs = append(runs, run)
	}
	s.data.Runs = runs

	steps := make([]domain.CollaborationStep, 0, len(s.data.CollaborationSteps))
	for _, step := range s.data.CollaborationSteps {
		if step.ConversationID == id || runIDs[step.RunID] {
			continue
		}
		steps = append(steps, step)
	}
	s.data.CollaborationSteps = steps

	runEvents := make([]domain.RunEvent, 0, len(s.data.RunEvents))
	for _, event := range s.data.RunEvents {
		if !runIDs[event.RunID] {
			runEvents = append(runEvents, event)
		}
	}
	s.data.RunEvents = runEvents

	checkpoints := make([]domain.StageCheckpoint, 0, len(s.data.StageCheckpoints))
	for _, checkpoint := range s.data.StageCheckpoints {
		if !runIDs[checkpoint.RunID] {
			checkpoints = append(checkpoints, checkpoint)
		}
	}
	s.data.StageCheckpoints = checkpoints

	effects := make([]domain.ToolEffectRecord, 0, len(s.data.ToolEffects))
	for _, effect := range s.data.ToolEffects {
		if !runIDs[effect.RunID] {
			effects = append(effects, effect)
		}
	}
	s.data.ToolEffects = effects

	usageEntries := make([]domain.RunUsageEntry, 0, len(s.data.RunUsageEntries))
	for _, entry := range s.data.RunUsageEntries {
		if !runIDs[entry.RunID] {
			usageEntries = append(usageEntries, entry)
		}
	}
	s.data.RunUsageEntries = usageEntries

	evidence := make([]domain.VerificationEvidence, 0, len(s.data.VerificationEvidence))
	for _, item := range s.data.VerificationEvidence {
		if !runIDs[item.RunID] {
			evidence = append(evidence, item)
		}
	}
	s.data.VerificationEvidence = evidence

	artifacts := make([]domain.VerificationArtifact, 0, len(s.data.VerificationArtifacts))
	for _, item := range s.data.VerificationArtifacts {
		if !runIDs[item.RunID] {
			artifacts = append(artifacts, item)
		}
	}
	s.data.VerificationArtifacts = artifacts

	compactions := make([]domain.ContextCompaction, 0, len(s.data.ContextCompactions))
	for _, compaction := range s.data.ContextCompactions {
		if compaction.ConversationID != id {
			compactions = append(compactions, compaction)
		}
	}
	s.data.ContextCompactions = compactions
	taskStateRevisions := make([]domain.TaskStateRevision, 0, len(s.data.TaskStateRevisions))
	for _, revision := range s.data.TaskStateRevisions {
		if revision.ConversationID != id {
			taskStateRevisions = append(taskStateRevisions, revision)
		}
	}
	s.data.TaskStateRevisions = taskStateRevisions
	modelRequests := make([]domain.ModelRequestRecord, 0, len(s.data.ModelRequestRecords))
	for _, record := range s.data.ModelRequestRecords {
		if !runIDs[record.Envelope.RunID] {
			modelRequests = append(modelRequests, record)
		}
	}
	s.data.ModelRequestRecords = modelRequests
	toolArtifactIDs := make([]string, 0)
	toolArtifacts := make([]domain.ToolArtifact, 0, len(s.data.ToolArtifacts))
	for _, artifact := range s.data.ToolArtifacts {
		if runIDs[artifact.RunID] {
			toolArtifactIDs = append(toolArtifactIDs, artifact.ID)
			continue
		}
		toolArtifacts = append(toolArtifacts, artifact)
	}
	s.data.ToolArtifacts = toolArtifacts

	for _, artifactID := range toolArtifactIDs {
		delete(s.artifactContent, artifactID)
	}
	return nil
}

func (s *Store) ListMessages(conversationID string) ([]domain.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	messages := []domain.Message{}
	for _, message := range s.data.Messages {
		if message.ConversationID == conversationID {
			message.Citations = store.CloneCitations(message.Citations)
			messages = append(messages, message)
		}
	}
	sort.Slice(messages, func(i, j int) bool {
		return messages[i].CreatedAt.Before(messages[j].CreatedAt)
	})
	return messages, nil
}

func (s *Store) AddMessage(conversationID string, role string, content string) (domain.Message, error) {
	return s.AddMessageWithCitations(conversationID, role, content, nil)
}

func (s *Store) AddMessageWithCitations(conversationID string, role string, content string, citations []domain.RAGCitation) (domain.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	conversation, ok := s.getConversationLocked(conversationID)
	if !ok {
		return domain.Message{}, errors.New("conversation not found")
	}

	now := time.Now().UTC()
	message := domain.Message{
		ID:             store.NewID("msg"),
		WorkspaceID:    conversation.WorkspaceID,
		ConversationID: conversationID,
		Role:           role,
		Content:        content,
		Citations:      store.CloneCitations(citations),
		CreatedAt:      now,
	}
	s.data.Messages = append(s.data.Messages, message)
	for i := range s.data.Conversations {
		if s.data.Conversations[i].ID == conversationID {
			s.data.Conversations[i].UpdatedAt = now
			break
		}
	}
	return message, nil
}

func (s *Store) ListMessagesInWorkspace(workspaceID string, conversationID string) ([]domain.Message, error) {
	if _, ok, err := s.GetConversationInWorkspace(workspaceID, conversationID); err != nil {
		return nil, err
	} else if !ok {
		return []domain.Message{}, nil
	}
	return s.ListMessages(conversationID)
}

func (s *Store) AddMessageInWorkspace(workspaceID string, conversationID string, role string, content string) (domain.Message, error) {
	return s.AddMessageWithCitationsInWorkspace(workspaceID, conversationID, role, content, nil)
}

func (s *Store) AddMessageWithCitationsInWorkspace(workspaceID string, conversationID string, role string, content string, citations []domain.RAGCitation) (domain.Message, error) {
	if _, ok, err := s.GetConversationInWorkspace(workspaceID, conversationID); err != nil {
		return domain.Message{}, err
	} else if !ok {
		return domain.Message{}, store.ErrNotFound("conversation")
	}
	return s.AddMessageWithCitations(conversationID, role, content, citations)
}

func (s *Store) CommitContextCompaction(compaction domain.ContextCompaction, completion domain.RunEvent) (domain.ContextCompaction, domain.RunEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasConversationLocked(compaction.ConversationID) {
		return domain.ContextCompaction{}, domain.RunEvent{}, store.ErrNotFound("conversation")
	}
	if !s.hasRunLocked(completion.RunID) {
		return domain.ContextCompaction{}, domain.RunEvent{}, store.ErrNotFound("run")
	}
	if completion.Type != domain.EventCompactionCompleted || completion.RunID != compaction.RunID || completion.ConversationID != compaction.ConversationID {
		return domain.ContextCompaction{}, domain.RunEvent{}, errors.New("compaction completion event does not match compaction")
	}
	for _, existing := range s.data.ContextCompactions {
		if existing.ConversationID == compaction.ConversationID && existing.SourceHash == compaction.SourceHash {
			return store.CloneContextCompaction(existing), domain.RunEvent{}, nil
		}
	}
	compaction = s.prepareContextCompactionLocked(compaction, domain.ContextCompactionCompleted)
	var next int64 = 1
	for _, existing := range s.data.RunEvents {
		if existing.RunID == completion.RunID && existing.Sequence >= next {
			next = existing.Sequence + 1
		}
	}
	preparedEvent, err := store.PrepareRunEvent(completion, next, compaction.CreatedAt)
	if err != nil {
		return domain.ContextCompaction{}, domain.RunEvent{}, err
	}
	s.data.ContextCompactions = append(s.data.ContextCompactions, compaction)
	s.data.RunEvents = append(s.data.RunEvents, preparedEvent)

	return store.CloneContextCompaction(compaction), preparedEvent, nil
}

func (s *Store) GetLatestContextCompaction(conversationID string) (domain.ContextCompaction, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var latest domain.ContextCompaction
	found := false
	for _, item := range s.data.ContextCompactions {
		if item.ConversationID != conversationID || !store.CompletedContextCompaction(item) {
			continue
		}
		if !found || item.CreatedAt.After(latest.CreatedAt) || (item.CreatedAt.Equal(latest.CreatedAt) && item.ID > latest.ID) {
			latest = item
			found = true
		}
	}
	return store.CloneContextCompaction(latest), found, nil
}

func (s *Store) prepareContextCompactionLocked(compaction domain.ContextCompaction, status domain.ContextCompactionStatus) domain.ContextCompaction {
	if compaction.ID == "" {
		compaction.ID = store.NewID("cmp")
	}
	if compaction.Generation <= 0 {
		for _, existing := range s.data.ContextCompactions {
			if existing.ConversationID == compaction.ConversationID && existing.Generation >= compaction.Generation {
				compaction.Generation = existing.Generation + 1
			}
		}
		if compaction.Generation <= 0 {
			compaction.Generation = 1
		}
	}
	if compaction.ReplacementSummaryID == "" {
		compaction.ReplacementSummaryID = "summary:" + compaction.ID
	}
	if compaction.CreatedAt.IsZero() {
		compaction.CreatedAt = time.Now().UTC()
	}
	compaction.Status = status
	if status == domain.ContextCompactionCompleted && compaction.SurfaceReplacedAt == nil {
		replacedAt := compaction.CreatedAt
		compaction.SurfaceReplacedAt = &replacedAt
	}
	compaction.SourceMessageIDs = append([]string(nil), compaction.SourceMessageIDs...)
	compaction.SourceEventIDs = append([]string(nil), compaction.SourceEventIDs...)
	return compaction
}

func (s *Store) CreateMemoryCandidate(candidate domain.MemoryCandidate) (domain.MemoryCandidate, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var err error
	candidate, err = store.NormalizeMemoryCandidate(candidate)
	if err != nil {
		return domain.MemoryCandidate{}, false, err
	}
	for _, existing := range s.data.MemoryCandidates {
		if existing.ID == candidate.ID {
			if existing.WorkspaceID != candidate.WorkspaceID {
				return domain.MemoryCandidate{}, false, store.ErrMemoryConflict
			}
			return existing, false, nil
		}
	}
	for _, memory := range s.data.MemoryChanges {
		if memory.SourceMessageID == candidate.SourceMessageID && memory.WorkspaceID == candidate.WorkspaceID {
			candidate = store.SuppressMemoryCandidate(candidate)
			break
		}
	}
	s.data.MemoryCandidates = append(s.data.MemoryCandidates, candidate)

	return candidate, true, nil
}

func (s *Store) ListMemoryCandidates(conversationID string) ([]domain.MemoryCandidate, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]domain.MemoryCandidate, 0, len(s.data.MemoryCandidates))
	for _, candidate := range s.data.MemoryCandidates {
		if conversationID == "" || candidate.ConversationID == conversationID {
			items = append(items, candidate)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items, nil
}

func (s *Store) UpdateConversationTitle(id string, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.data.Conversations {
		if s.data.Conversations[i].ID == id {
			s.data.Conversations[i].Title = store.NormalizeTitle(title)
			s.data.Conversations[i].UpdatedAt = time.Now().UTC()
			return nil
		}
	}
	return store.ErrNotFound("conversation")
}

func (s *Store) UpdateConversationTitleInWorkspace(workspaceID string, id string, title string) error {
	if _, ok, err := s.GetConversationInWorkspace(workspaceID, id); err != nil {
		return err
	} else if !ok {
		return store.ErrNotFound("conversation")
	}
	return s.UpdateConversationTitle(id, title)
}
