package store

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/failure"
)

var ErrDocumentVersionConflict = failure.New(failure.Definition{
	Message: "document version already identifies different content",
	Info: failure.Info{
		Code: "document_version_conflict", Source: "knowledge_index", Category: failure.CategoryValidation,
	},
})

func IsDocumentVersionConflict(err error) bool {
	return errors.Is(err, ErrDocumentVersionConflict)
}

func prepareDocumentWrite(document domain.Document, chunks []domain.DocumentChunk, embeddings []domain.DocumentChunkEmbedding) (domain.Document, []domain.DocumentChunk, []domain.DocumentChunkEmbedding, error) {
	if len(chunks) != len(embeddings) {
		return domain.Document{}, nil, nil, errors.New("document chunks and embeddings length mismatch")
	}
	now := time.Now().UTC()
	document.WorkspaceID = normalizeWorkspaceID(document.WorkspaceID)
	document.ID = strings.TrimSpace(document.ID)
	if document.ID == "" {
		document.ID = newID("doc")
	}
	document.SourceKey = strings.TrimSpace(document.SourceKey)
	document.SourceURI = strings.TrimSpace(document.SourceURI)
	if document.SourceKey == "" {
		document.SourceKey = document.SourceURI
	}
	if len(document.SourceKey) > 512 {
		return domain.Document{}, nil, nil, errors.New("document source key must not exceed 512 bytes")
	}
	document.Title = strings.TrimSpace(document.Title)
	if document.Title == "" {
		return domain.Document{}, nil, nil, errors.New("document title is required")
	}
	document.Content = strings.TrimSpace(document.Content)
	if document.Content == "" {
		return domain.Document{}, nil, nil, errors.New("document content is required")
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

	nonEmptyEmbeddings := 0
	for i := range chunks {
		chunks[i].ID = strings.TrimSpace(chunks[i].ID)
		if chunks[i].ID == "" {
			chunks[i].ID = newID("chunk")
		}
		chunks[i].DocumentID = document.ID
		chunks[i].ChunkIndex = i
		chunks[i].Content = strings.TrimSpace(chunks[i].Content)
		if chunks[i].Content == "" {
			return domain.Document{}, nil, nil, errors.New("document chunk content is required")
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
			embeddings[i].Model = "local_hash_embedding"
		}
		if embeddings[i].Dimensions == 0 {
			embeddings[i].Dimensions = len(embeddings[i].Embedding)
		}
		if len(embeddings[i].Embedding) > 0 {
			nonEmptyEmbeddings++
		}
		if len(embeddings[i].Embedding) > 0 && embeddings[i].Dimensions != len(embeddings[i].Embedding) {
			return domain.Document{}, nil, nil, fmt.Errorf("document chunk embedding dimensions are %d, vector has %d", embeddings[i].Dimensions, len(embeddings[i].Embedding))
		}
		if embeddings[i].CreatedAt.IsZero() {
			embeddings[i].CreatedAt = now
		}
	}

	if nonEmptyEmbeddings != 0 && nonEmptyEmbeddings != len(embeddings) {
		return domain.Document{}, nil, nil, errors.New("document chunk embeddings must be either all present or all absent")
	}
	if nonEmptyEmbeddings > 0 {
		actual := domain.DocumentIndexIdentity{
			ChunkerVersion: domain.DocumentChunkerVersion, EmbeddingProvider: embeddings[0].Provider,
			EmbeddingModel: embeddings[0].Model, EmbeddingDimensions: embeddings[0].Dimensions,
		}
		for _, embedding := range embeddings[1:] {
			if embedding.Provider != actual.EmbeddingProvider || embedding.Model != actual.EmbeddingModel || embedding.Dimensions != actual.EmbeddingDimensions {
				return domain.Document{}, nil, nil, errors.New("document chunk embeddings use incompatible index identities")
			}
		}
		if isZeroDocumentIndexIdentity(document.IndexIdentity) {
			document.IndexIdentity = actual
		} else if document.IndexIdentity != actual {
			return domain.Document{}, nil, nil, errors.New("document index identity does not match chunk embeddings")
		}
	} else if !isZeroDocumentIndexIdentity(document.IndexIdentity) {
		return domain.Document{}, nil, nil, errors.New("document index identity requires chunk embeddings")
	}
	document.ChunkCount = len(chunks)
	document.EmbeddingCount = len(embeddings)
	return document, chunks, embeddings, nil
}

func bindDocumentID(document *domain.Document, chunks []domain.DocumentChunk, embeddings []domain.DocumentChunkEmbedding, id string) {
	document.ID = id
	for index := range chunks {
		chunks[index].DocumentID = id
		embeddings[index].ChunkID = chunks[index].ID
	}
}

func isZeroDocumentIndexIdentity(identity domain.DocumentIndexIdentity) bool {
	return identity == (domain.DocumentIndexIdentity{})
}

func documentVersionConflict(existing, incoming domain.Document) error {
	return fmt.Errorf("%w: source_key=%q version=%q", ErrDocumentVersionConflict, incoming.SourceKey, incoming.Version)
}

func normalizeFileDocumentIndexes(data *fileData) bool {
	migrated := false
	chunkDocuments := make(map[string]string, len(data.DocumentChunks))
	for _, chunk := range data.DocumentChunks {
		chunkDocuments[chunk.ID] = chunk.DocumentID
	}
	identities := map[string]domain.DocumentIndexIdentity{}
	incompatible := map[string]bool{}
	for _, embedding := range data.ChunkEmbeddings {
		documentID := chunkDocuments[embedding.ChunkID]
		if documentID == "" || len(embedding.Embedding) == 0 {
			continue
		}
		dimensions := embedding.Dimensions
		if dimensions == 0 {
			dimensions = len(embedding.Embedding)
		}
		identity := domain.DocumentIndexIdentity{
			ChunkerVersion: domain.DocumentChunkerVersion, EmbeddingProvider: embedding.Provider,
			EmbeddingModel: embedding.Model, EmbeddingDimensions: dimensions,
		}
		if existing, ok := identities[documentID]; ok && existing != identity {
			incompatible[documentID] = true
		} else {
			identities[documentID] = identity
		}
	}

	latestBySource := map[string]int{}
	for index := range data.Documents {
		document := &data.Documents[index]
		if document.SourceKey == "" && strings.TrimSpace(document.SourceURI) != "" {
			document.SourceKey = strings.TrimSpace(document.SourceURI)
			migrated = true
		}
		if isZeroDocumentIndexIdentity(document.IndexIdentity) && !incompatible[document.ID] && !isZeroDocumentIndexIdentity(identities[document.ID]) {
			document.IndexIdentity = identities[document.ID]
			migrated = true
		}
		if document.SourceKey == "" {
			continue
		}
		key := normalizeWorkspaceID(document.WorkspaceID) + "\x00" + document.SourceKey
		if previous, ok := latestBySource[key]; !ok || newerDocument(*document, data.Documents[previous]) {
			latestBySource[key] = index
		}
	}

	keep := make(map[string]bool, len(data.Documents))
	for index, document := range data.Documents {
		if document.SourceKey == "" {
			keep[document.ID] = true
			continue
		}
		key := normalizeWorkspaceID(document.WorkspaceID) + "\x00" + document.SourceKey
		if latestBySource[key] == index {
			keep[document.ID] = true
		} else {
			migrated = true
		}
	}
	if len(keep) == len(data.Documents) {
		return migrated
	}

	documents := make([]domain.Document, 0, len(keep))
	for _, document := range data.Documents {
		if keep[document.ID] {
			documents = append(documents, document)
		} else {
			delete(data.DocumentContents, document.ID)
		}
	}
	chunks := make([]domain.DocumentChunk, 0, len(data.DocumentChunks))
	keptChunks := map[string]bool{}
	for _, chunk := range data.DocumentChunks {
		if keep[chunk.DocumentID] {
			chunks = append(chunks, chunk)
			keptChunks[chunk.ID] = true
		}
	}
	embeddings := make([]domain.DocumentChunkEmbedding, 0, len(data.ChunkEmbeddings))
	for _, embedding := range data.ChunkEmbeddings {
		if keptChunks[embedding.ChunkID] {
			embeddings = append(embeddings, embedding)
		}
	}
	data.Documents, data.DocumentChunks, data.ChunkEmbeddings = documents, chunks, embeddings
	return migrated
}

func newerDocument(left, right domain.Document) bool {
	if !left.UpdatedAt.Equal(right.UpdatedAt) {
		return left.UpdatedAt.After(right.UpdatedAt)
	}
	if !left.CreatedAt.Equal(right.CreatedAt) {
		return left.CreatedAt.After(right.CreatedAt)
	}
	return left.ID > right.ID
}
