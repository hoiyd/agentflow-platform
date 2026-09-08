package fixturestore

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/store"
	"errors"
	"sort"
	"strings"
	"time"
)

func (s *Store) CreateDocument(document domain.Document, chunks []domain.DocumentChunk, embeddings []domain.DocumentChunkEmbedding) (domain.Document, error) {
	document, chunks, embeddings, err := store.PrepareDocumentWrite(document, chunks, embeddings)
	if err != nil {
		return domain.Document{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	replacedDocumentIDs := map[string]bool{}
	var existing *domain.Document
	for index := range s.data.Documents {
		candidate := &s.data.Documents[index]
		if document.SourceKey == "" || candidate.WorkspaceID != document.WorkspaceID || candidate.SourceKey != document.SourceKey {
			continue
		}
		replacedDocumentIDs[candidate.ID] = true
		if existing == nil || candidate.UpdatedAt.After(existing.UpdatedAt) {
			copy := *candidate
			existing = &copy
		}
	}
	if existing != nil {
		if existing.Version == document.Version && existing.ContentHash != document.ContentHash {
			return domain.Document{}, store.DocumentVersionConflict(*existing, document)
		}
		if len(replacedDocumentIDs) == 1 && existing.Version == document.Version && existing.ContentHash == document.ContentHash && existing.IndexIdentity == document.IndexIdentity {
			return *existing, nil
		}
		document.CreatedAt = existing.CreatedAt
		store.BindDocumentID(&document, chunks, embeddings, existing.ID)
	}

	next := s.data
	next.Documents = make([]domain.Document, 0, len(s.data.Documents)+1)
	for _, candidate := range s.data.Documents {
		if !replacedDocumentIDs[candidate.ID] {
			next.Documents = append(next.Documents, candidate)
		}
	}
	next.Documents = append(next.Documents, document)
	next.DocumentContents = make(map[string]string, len(s.data.DocumentContents)+1)
	for id, content := range s.data.DocumentContents {
		if !replacedDocumentIDs[id] {
			next.DocumentContents[id] = content
		}
	}
	next.DocumentContents[document.ID] = document.Content

	deletedChunkIDs := map[string]bool{}
	next.DocumentChunks = make([]domain.DocumentChunk, 0, len(s.data.DocumentChunks)+len(chunks))
	for _, chunk := range s.data.DocumentChunks {
		if replacedDocumentIDs[chunk.DocumentID] {
			deletedChunkIDs[chunk.ID] = true
			continue
		}
		next.DocumentChunks = append(next.DocumentChunks, chunk)
	}
	next.DocumentChunks = append(next.DocumentChunks, chunks...)
	next.ChunkEmbeddings = make([]domain.DocumentChunkEmbedding, 0, len(s.data.ChunkEmbeddings)+len(embeddings))
	for _, embedding := range s.data.ChunkEmbeddings {
		if !deletedChunkIDs[embedding.ChunkID] {
			next.ChunkEmbeddings = append(next.ChunkEmbeddings, embedding)
		}
	}
	next.ChunkEmbeddings = append(next.ChunkEmbeddings, embeddings...)

	s.data = next

	return document, nil
}

func (s *Store) ListDocumentIndexIdentities(workspaceID string) ([]domain.DocumentIndexIdentity, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	workspaceID = store.NormalizeWorkspaceID(workspaceID)
	seen := map[domain.DocumentIndexIdentity]bool{}
	items := []domain.DocumentIndexIdentity{}
	for _, document := range s.data.Documents {
		if document.WorkspaceID == workspaceID && !seen[document.IndexIdentity] {
			seen[document.IndexIdentity] = true
			items = append(items, document.IndexIdentity)
		}
	}
	return items, nil
}

func (s *Store) ListDocuments() ([]domain.Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	documents := append([]domain.Document(nil), s.data.Documents...)
	chunkCounts := map[string]int{}
	embeddingCounts := map[string]int{}
	for _, chunk := range s.data.DocumentChunks {
		chunkCounts[chunk.DocumentID]++
	}
	chunkIDToDocumentID := map[string]string{}
	for _, chunk := range s.data.DocumentChunks {
		chunkIDToDocumentID[chunk.ID] = chunk.DocumentID
	}
	for _, embedding := range s.data.ChunkEmbeddings {
		if documentID := chunkIDToDocumentID[embedding.ChunkID]; documentID != "" {
			embeddingCounts[documentID]++
		}
	}
	for i := range documents {
		documents[i].ChunkCount = chunkCounts[documents[i].ID]
		documents[i].EmbeddingCount = embeddingCounts[documents[i].ID]
	}
	sort.Slice(documents, func(i, j int) bool {
		return documents[i].CreatedAt.After(documents[j].CreatedAt)
	})
	return documents, nil
}

func (s *Store) ListDocumentsByWorkspace(workspaceID string) ([]domain.Document, error) {
	documents, err := s.ListDocuments()
	if err != nil {
		return nil, err
	}
	workspaceID = store.NormalizeWorkspaceID(workspaceID)
	items := make([]domain.Document, 0, len(documents))
	for _, document := range documents {
		if document.WorkspaceID == workspaceID {
			items = append(items, document)
		}
	}
	return items, nil
}

func (s *Store) GetDocument(id string) (domain.Document, []domain.DocumentChunk, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var document domain.Document
	found := false
	for _, item := range s.data.Documents {
		if item.ID == strings.TrimSpace(id) {
			document = item
			found = true
			break
		}
	}
	if !found {
		return domain.Document{}, nil, false, nil
	}
	chunks := []domain.DocumentChunk{}
	for _, chunk := range s.data.DocumentChunks {
		if chunk.DocumentID == document.ID {
			chunk.Document = document
			chunks = append(chunks, chunk)
		}
	}
	sort.Slice(chunks, func(i, j int) bool {
		return chunks[i].ChunkIndex < chunks[j].ChunkIndex
	})
	document.ChunkCount = len(chunks)
	embeddingByChunkID := map[string]bool{}
	for _, embedding := range s.data.ChunkEmbeddings {
		embeddingByChunkID[embedding.ChunkID] = true
	}
	for _, chunk := range chunks {
		if embeddingByChunkID[chunk.ID] {
			document.EmbeddingCount++
		}
	}
	return document, chunks, true, nil
}

func (s *Store) GetDocumentInWorkspace(workspaceID string, id string) (domain.Document, []domain.DocumentChunk, bool, error) {
	document, chunks, ok, err := s.GetDocument(id)
	if err != nil || !ok || document.WorkspaceID != store.NormalizeWorkspaceID(workspaceID) {
		return domain.Document{}, nil, false, err
	}
	return document, chunks, true, nil
}

func (s *Store) DeleteDocument(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	id = strings.TrimSpace(id)
	if id == "" {
		return store.ErrNotFound("document")
	}
	found := false
	documents := s.data.Documents[:0]
	for _, document := range s.data.Documents {
		if document.ID == id {
			found = true
			continue
		}
		documents = append(documents, document)
	}
	if !found {
		return store.ErrNotFound("document")
	}

	deletedChunkIDs := map[string]bool{}
	chunks := s.data.DocumentChunks[:0]
	for _, chunk := range s.data.DocumentChunks {
		if chunk.DocumentID == id {
			deletedChunkIDs[chunk.ID] = true
			continue
		}
		chunks = append(chunks, chunk)
	}
	embeddings := s.data.ChunkEmbeddings[:0]
	for _, embedding := range s.data.ChunkEmbeddings {
		if deletedChunkIDs[embedding.ChunkID] {
			continue
		}
		embeddings = append(embeddings, embedding)
	}

	s.data.Documents = documents
	delete(s.data.DocumentContents, id)
	s.data.DocumentChunks = chunks
	s.data.ChunkEmbeddings = embeddings
	return nil
}

func (s *Store) DeleteDocumentInWorkspace(workspaceID string, id string) error {
	if _, _, ok, err := s.GetDocumentInWorkspace(workspaceID, id); err != nil {
		return err
	} else if !ok {
		return store.ErrNotFound("document")
	}
	return s.DeleteDocument(id)
}

func (s *Store) SearchDocumentChunks(search domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	search.WorkspaceID = store.NormalizeWorkspaceID(search.WorkspaceID)

	limit := search.Limit
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}
	documentByID := map[string]domain.Document{}
	for _, document := range s.data.Documents {
		documentByID[document.ID] = document
	}
	embeddingByChunkID := map[string]domain.DocumentChunkEmbedding{}
	for _, embedding := range s.data.ChunkEmbeddings {
		embeddingByChunkID[embedding.ChunkID] = embedding
	}

	items := []domain.RetrievedDocumentChunk{}
	now := time.Now().UTC()
	for _, chunk := range s.data.DocumentChunks {
		document, ok := documentByID[chunk.DocumentID]
		if !ok || !store.DocumentChunkMatchesSearch(document, chunk, search) {
			continue
		}
		embedding, ok := embeddingByChunkID[chunk.ID]
		if !ok || len(embedding.Embedding) == 0 || len(search.Embedding) == 0 {
			continue
		}
		if search.EmbeddingProvider != "" && embedding.Provider != search.EmbeddingProvider {
			continue
		}
		if search.EmbeddingModel != "" && embedding.Model != search.EmbeddingModel {
			continue
		}
		similarity := store.CosineSimilarity(search.Embedding, embedding.Embedding)
		if search.MinSimilarity > 0 && similarity < search.MinSimilarity {
			continue
		}
		recencyBoost := store.MemoryRecencyBoost(now, chunk.CreatedAt)
		items = append(items, domain.RetrievedDocumentChunk{
			Document:     document,
			Chunk:        chunk,
			Similarity:   similarity,
			RecencyBoost: recencyBoost,
			Score:        similarity + recencyBoost,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Score > items[j].Score
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *Store) SearchDocumentChunksLexical(search domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	search.WorkspaceID = store.NormalizeWorkspaceID(search.WorkspaceID)

	limit := search.Limit
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}
	documentByID := make(map[string]domain.Document, len(s.data.Documents))
	for _, document := range s.data.Documents {
		documentByID[document.ID] = document
	}

	items := []domain.RetrievedDocumentChunk{}
	now := time.Now().UTC()
	for _, chunk := range s.data.DocumentChunks {
		document, ok := documentByID[chunk.DocumentID]
		if !ok || !store.DocumentChunkMatchesSearch(document, chunk, search) {
			continue
		}
		lexicalScore := store.DocumentChunkLexicalScore(search, document, chunk)
		if lexicalScore <= 0 {
			continue
		}
		recencyBoost := store.MemoryRecencyBoost(now, chunk.CreatedAt)
		items = append(items, domain.RetrievedDocumentChunk{
			Document:     document,
			Chunk:        chunk,
			RecencyBoost: recencyBoost,
			Score:        lexicalScore + recencyBoost,
			LexicalScore: lexicalScore,
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].LexicalScore == items[j].LexicalScore {
			return items[i].RecencyBoost > items[j].RecencyBoost
		}
		return items[i].LexicalScore > items[j].LexicalScore
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *Store) ListDocumentContextChunks(search domain.DocumentContextSearch) ([]domain.RetrievedDocumentChunk, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	search.WorkspaceID = store.NormalizeWorkspaceID(search.WorkspaceID)

	documentID := strings.TrimSpace(search.DocumentID)
	if documentID == "" {
		return nil, errors.New("document context search document ID is required")
	}
	var document domain.Document
	found := false
	for _, candidate := range s.data.Documents {
		if candidate.ID == documentID {
			document = candidate
			found = true
			break
		}
	}
	if !found || store.NormalizeWorkspaceID(document.WorkspaceID) != search.WorkspaceID {
		return []domain.RetrievedDocumentChunk{}, nil
	}

	filter := domain.DocumentSearch{WorkspaceID: search.WorkspaceID, Metadata: search.Metadata}
	items := make([]domain.RetrievedDocumentChunk, 0)
	for _, chunk := range s.data.DocumentChunks {
		if chunk.DocumentID != documentID || !store.DocumentChunkMatchesSearch(document, chunk, filter) {
			continue
		}
		sameParent := strings.TrimSpace(search.ParentID) != "" && chunk.ParentID == strings.TrimSpace(search.ParentID)
		adjacent := search.NeighborWindow > 0 && store.AbsInt(chunk.ChunkIndex-search.ChunkIndex) <= search.NeighborWindow
		if !sameParent && !adjacent {
			continue
		}
		chunk.Document = document
		items = append(items, domain.RetrievedDocumentChunk{Document: document, Chunk: chunk})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Chunk.ChunkIndex < items[j].Chunk.ChunkIndex
	})
	return items, nil
}
