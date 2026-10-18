package fixturestore

import (
	"strings"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
)

func (s *Store) GetDocumentBySource(workspaceID, sourceKey string) (domain.Document, bool, error) {
	items, err := s.ListDocumentsByWorkspace(workspaceID)
	if err != nil {
		return domain.Document{}, false, err
	}
	for _, item := range items {
		if item.SourceKey == strings.TrimSpace(sourceKey) {
			return item, true, nil
		}
	}
	return domain.Document{}, false, nil
}

func (s *Store) UpdateDocumentMetadata(workspaceID, id string, update domain.DocumentMetadataUpdate) (domain.Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, document := range s.data.Documents {
		if document.ID != strings.TrimSpace(id) || document.WorkspaceID != store.NormalizeWorkspaceID(workspaceID) {
			continue
		}
		change, err := store.PrepareDocumentMetadataChange(document, update)
		if err != nil {
			return domain.Document{}, err
		}
		if change.UpdatedAt.Equal(document.UpdatedAt) {
			return document, nil
		}
		document.Metadata, document.UpdatedAt = change.Metadata, change.UpdatedAt
		for j, chunk := range s.data.DocumentChunks {
			if chunk.DocumentID == document.ID {
				s.data.DocumentChunks[j].Metadata = change.ChunkMetadata(chunk.Metadata)
			}
		}
		s.data.Documents[i] = document
		return document, nil
	}
	return domain.Document{}, store.ErrNotFound("document")
}
