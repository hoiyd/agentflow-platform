package store

import (
	"errors"
	"sort"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func (s *FileStore) CreateDocument(document domain.Document, chunks []domain.DocumentChunk, embeddings []domain.DocumentChunkEmbedding) (domain.Document, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(chunks) != len(embeddings) {
		return domain.Document{}, errors.New("document chunks and embeddings length mismatch")
	}
	now := time.Now().UTC()
	document.ID = strings.TrimSpace(document.ID)
	if document.ID == "" {
		document.ID = newID("doc")
	}
	document.Title = strings.TrimSpace(document.Title)
	if document.Title == "" {
		return domain.Document{}, errors.New("document title is required")
	}
	document.Content = strings.TrimSpace(document.Content)
	if document.Content == "" {
		return domain.Document{}, errors.New("document content is required")
	}
	document.SourceType = strings.TrimSpace(document.SourceType)
	if document.SourceType == "" {
		document.SourceType = "text"
	}
	if document.Metadata == nil {
		document.Metadata = map[string]any{}
	}
	if document.CreatedAt.IsZero() {
		document.CreatedAt = now
	}
	document.UpdatedAt = now

	for i := range chunks {
		chunks[i].ID = strings.TrimSpace(chunks[i].ID)
		if chunks[i].ID == "" {
			chunks[i].ID = newID("chunk")
		}
		chunks[i].DocumentID = document.ID
		chunks[i].ChunkIndex = i
		chunks[i].Content = strings.TrimSpace(chunks[i].Content)
		if chunks[i].Content == "" {
			return domain.Document{}, errors.New("document chunk content is required")
		}
		if chunks[i].Metadata == nil {
			chunks[i].Metadata = map[string]any{}
		}
		if chunks[i].SectionPath == nil {
			chunks[i].SectionPath = []string{}
		}
		if chunks[i].DocumentVersion == "" {
			chunks[i].DocumentVersion = document.Version
		}
		if chunks[i].CreatedAt.IsZero() {
			chunks[i].CreatedAt = now
		}
		embeddings[i].ChunkID = chunks[i].ID
		if embeddings[i].Provider == "" {
			embeddings[i].Provider = "local"
		}
		if embeddings[i].Model == "" {
			embeddings[i].Model = "local_hash"
		}
		if embeddings[i].Dimensions == 0 {
			embeddings[i].Dimensions = len(embeddings[i].Embedding)
		}
		if embeddings[i].CreatedAt.IsZero() {
			embeddings[i].CreatedAt = now
		}
	}

	document.ChunkCount = len(chunks)
	document.EmbeddingCount = len(embeddings)
	if s.data.DocumentContents == nil {
		s.data.DocumentContents = map[string]string{}
	}
	s.data.DocumentContents[document.ID] = document.Content
	s.data.Documents = append(s.data.Documents, document)
	s.data.DocumentChunks = append(s.data.DocumentChunks, chunks...)
	s.data.ChunkEmbeddings = append(s.data.ChunkEmbeddings, embeddings...)
	return document, s.saveLocked()
}

func (s *FileStore) ListDocuments() ([]domain.Document, error) {
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

func (s *FileStore) GetDocument(id string) (domain.Document, []domain.DocumentChunk, bool, error) {
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

func (s *FileStore) DeleteDocument(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	id = strings.TrimSpace(id)
	if id == "" {
		return ErrNotFound("document")
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
		return ErrNotFound("document")
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
	return s.saveLocked()
}

func (s *FileStore) SearchDocumentChunks(search domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

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
		if !ok || !documentChunkMatchesSearch(document, chunk, search) {
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
		similarity := cosineSimilarity(search.Embedding, embedding.Embedding)
		if search.MinSimilarity > 0 && similarity < search.MinSimilarity {
			continue
		}
		recencyBoost := memoryRecencyBoost(now, chunk.CreatedAt)
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

func (s *FileStore) SearchDocumentChunksLexical(search domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

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
		if !ok || !documentChunkMatchesSearch(document, chunk, search) {
			continue
		}
		lexicalScore := documentChunkLexicalScore(search, document, chunk)
		if lexicalScore <= 0 {
			continue
		}
		recencyBoost := memoryRecencyBoost(now, chunk.CreatedAt)
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

func (s *FileStore) ListDocumentContextChunks(search domain.DocumentContextSearch) ([]domain.RetrievedDocumentChunk, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

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
	if !found || (strings.TrimSpace(search.WorkspaceID) != "" && document.WorkspaceID != strings.TrimSpace(search.WorkspaceID)) {
		return []domain.RetrievedDocumentChunk{}, nil
	}

	filter := domain.DocumentSearch{WorkspaceID: search.WorkspaceID, Metadata: search.Metadata}
	items := make([]domain.RetrievedDocumentChunk, 0)
	for _, chunk := range s.data.DocumentChunks {
		if chunk.DocumentID != documentID || !documentChunkMatchesSearch(document, chunk, filter) {
			continue
		}
		sameParent := strings.TrimSpace(search.ParentID) != "" && chunk.ParentID == strings.TrimSpace(search.ParentID)
		adjacent := search.NeighborWindow > 0 && absInt(chunk.ChunkIndex-search.ChunkIndex) <= search.NeighborWindow
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

func documentChunkMatchesSearch(document domain.Document, chunk domain.DocumentChunk, search domain.DocumentSearch) bool {
	if search.WorkspaceID != "" && document.WorkspaceID != search.WorkspaceID {
		return false
	}
	for key, expected := range search.Metadata {
		value, ok := chunk.Metadata[key]
		if !ok {
			value, ok = document.Metadata[key]
		}
		if !ok || strings.TrimSpace(expected) != strings.TrimSpace(toString(value)) {
			return false
		}
	}
	return true
}

func documentChunkLexicalScore(search domain.DocumentSearch, document domain.Document, chunk domain.DocumentChunk) float64 {
	query := strings.ToLower(strings.TrimSpace(search.Query))
	if query == "" {
		return 0
	}
	text := strings.ToLower(strings.Join([]string{
		document.Title,
		document.SourceURI,
		chunk.Content,
		toString(document.Metadata["filename"]),
		toString(chunk.Metadata["title"]),
		toString(chunk.Metadata["heading_path"]),
	}, " "))
	score := 0.0
	if strings.Contains(text, query) {
		score = 1
	}
	if lexicalIdentifierMatch(query, text) {
		score = 1
	}
	if len(search.LexicalTerms) > 0 {
		matches := 0
		for _, term := range search.LexicalTerms {
			if strings.Contains(text, strings.ToLower(strings.TrimSpace(term))) {
				matches++
			}
		}
		coverage := float64(matches) / float64(len(search.LexicalTerms))
		if candidate := coverage * 0.60; candidate > score {
			score = candidate
		}
	}
	if score > 1 {
		return 1
	}
	return score
}

func lexicalIdentifierMatch(query string, text string) bool {
	for _, token := range strings.Fields(query) {
		token = strings.Trim(token, ".,;:!?()[]{}<>\"'`，。；：！？（）【】")
		if len([]rune(token)) < 3 || !containsASCIIDigit(token) {
			continue
		}
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

func containsASCIIDigit(value string) bool {
	for _, r := range value {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
