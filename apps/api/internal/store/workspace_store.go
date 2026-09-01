package store

import "agentflow-platform/apps/api/internal/domain"

type workspaceStore struct {
	backend     Store
	workspaceID string
}

func (s *FileStore) ForWorkspace(scope domain.WorkspaceScope) WorkspaceStore {
	return workspaceStore{backend: s, workspaceID: scope.ID()}
}

func (s *PostgresStore) ForWorkspace(scope domain.WorkspaceScope) WorkspaceStore {
	return workspaceStore{backend: s, workspaceID: scope.ID()}
}

func (s workspaceStore) ListConversations() ([]domain.Conversation, error) {
	return s.backend.ListConversationsByWorkspace(s.workspaceID)
}

func (s workspaceStore) CreateConversation(title string) (domain.Conversation, error) {
	return s.backend.CreateConversationInWorkspace(s.workspaceID, title)
}

func (s workspaceStore) GetConversation(id string) (domain.Conversation, bool, error) {
	return s.backend.GetConversationInWorkspace(s.workspaceID, id)
}

func (s workspaceStore) DeleteConversation(id string) error {
	return s.backend.DeleteConversationInWorkspace(s.workspaceID, id)
}

func (s workspaceStore) ListMessages(conversationID string) ([]domain.Message, error) {
	return s.backend.ListMessagesInWorkspace(s.workspaceID, conversationID)
}

func (s workspaceStore) AddMessage(conversationID string, role string, content string) (domain.Message, error) {
	return s.backend.AddMessageInWorkspace(s.workspaceID, conversationID, role, content)
}

func (s workspaceStore) AddMessageWithCitations(conversationID string, role string, content string, citations []domain.RAGCitation) (domain.Message, error) {
	return s.backend.AddMessageWithCitationsInWorkspace(s.workspaceID, conversationID, role, content, citations)
}

func (s workspaceStore) UpdateConversationTitle(id string, title string) error {
	return s.backend.UpdateConversationTitleInWorkspace(s.workspaceID, id, title)
}

func (s workspaceStore) GetRun(id string) (domain.Run, bool, error) {
	return s.backend.GetRunInWorkspace(s.workspaceID, id)
}

func (s workspaceStore) ListRuns() ([]domain.Run, error) {
	return s.backend.ListRunsByWorkspace(s.workspaceID)
}

func (s workspaceStore) ListCollaborationSteps(runID string) ([]domain.CollaborationStep, error) {
	if err := s.requireRun(runID); err != nil {
		return nil, err
	}
	return s.backend.ListCollaborationSteps(runID)
}

func (s workspaceStore) ListRunEvents(runID string) ([]domain.RunEvent, error) {
	if err := s.requireRun(runID); err != nil {
		return nil, err
	}
	return s.backend.ListRunEvents(runID)
}

func (s workspaceStore) CreateRunEvent(event domain.RunEvent) (domain.RunEvent, error) {
	if err := s.requireRun(event.RunID); err != nil {
		return domain.RunEvent{}, err
	}
	return s.backend.CreateRunEvent(event)
}

func (s workspaceStore) GetRunReplay(runID string) (domain.RunReplay, bool, error) {
	if err := s.requireRun(runID); err != nil {
		if IsNotFound(err) {
			return domain.RunReplay{}, false, nil
		}
		return domain.RunReplay{}, false, err
	}
	return s.backend.GetRunReplay(runID)
}

func (s workspaceStore) GetRunUsageLedger(runID string) (domain.RunUsageLedger, bool, error) {
	if err := s.requireRun(runID); err != nil {
		if IsNotFound(err) {
			return domain.RunUsageLedger{}, false, nil
		}
		return domain.RunUsageLedger{}, false, err
	}
	return s.backend.GetRunUsageLedger(runID)
}

func (s workspaceStore) GetTaskState(conversationID string) (domain.TaskState, bool, error) {
	if err := s.requireConversation(conversationID); err != nil {
		return domain.TaskState{}, false, err
	}
	return s.backend.GetTaskState(conversationID)
}

func (s workspaceStore) GetTaskStateRevision(conversationID string, version int64) (domain.TaskStateRevision, bool, error) {
	if err := s.requireConversation(conversationID); err != nil {
		return domain.TaskStateRevision{}, false, err
	}
	return s.backend.GetTaskStateRevision(conversationID, version)
}

func (s workspaceStore) ListTaskStateRevisions(conversationID string) ([]domain.TaskStateRevision, error) {
	if err := s.requireConversation(conversationID); err != nil {
		return nil, err
	}
	return s.backend.ListTaskStateRevisions(conversationID)
}

func (s workspaceStore) ApplyTaskStatePatch(conversationID string, patch domain.TaskStatePatch, source domain.TaskStateSource) (domain.TaskStateRevision, error) {
	if err := s.requireConversation(conversationID); err != nil {
		return domain.TaskStateRevision{}, err
	}
	return s.backend.ApplyTaskStatePatch(conversationID, patch, source)
}

func (s workspaceStore) ListModelRequestRecords(runID string) ([]domain.ModelRequestRecord, error) {
	if err := s.requireRun(runID); err != nil {
		return nil, err
	}
	return s.backend.ListModelRequestRecords(runID)
}

func (s workspaceStore) ListToolArtifacts(runID string) ([]domain.ToolArtifact, error) {
	if err := s.requireRun(runID); err != nil {
		return nil, err
	}
	return s.backend.ListToolArtifacts(runID)
}

func (s workspaceStore) ReadToolArtifact(runID string, artifactID string, offset int, limit int) (domain.ToolArtifactRead, error) {
	if err := s.requireRun(runID); err != nil {
		return domain.ToolArtifactRead{}, err
	}
	return s.backend.ReadToolArtifact(runID, artifactID, offset, limit)
}

func (s workspaceStore) SearchToolArtifact(runID string, artifactID string, query string, maxMatches int) (domain.ToolArtifactSearchResult, error) {
	if err := s.requireRun(runID); err != nil {
		return domain.ToolArtifactSearchResult{}, err
	}
	return s.backend.SearchToolArtifact(runID, artifactID, query, maxMatches)
}

func (s workspaceStore) UpdateRunVerificationStatus(runID string, status domain.VerificationStatus) (domain.Run, error) {
	if err := s.requireRun(runID); err != nil {
		return domain.Run{}, err
	}
	return s.backend.UpdateRunVerificationStatus(runID, status)
}

func (s workspaceStore) ListDocuments() ([]domain.Document, error) {
	return s.backend.ListDocumentsByWorkspace(s.workspaceID)
}

func (s workspaceStore) GetDocument(id string) (domain.Document, []domain.DocumentChunk, bool, error) {
	return s.backend.GetDocumentInWorkspace(s.workspaceID, id)
}

func (s workspaceStore) DeleteDocument(id string) error {
	return s.backend.DeleteDocumentInWorkspace(s.workspaceID, id)
}

func (s workspaceStore) requireRun(runID string) error {
	_, ok, err := s.backend.GetRunInWorkspace(s.workspaceID, runID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound("run")
	}
	return nil
}

func (s workspaceStore) requireConversation(conversationID string) error {
	_, ok, err := s.backend.GetConversationInWorkspace(s.workspaceID, conversationID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound("conversation")
	}
	return nil
}
