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

func PrepareDocumentWrite(document domain.Document, chunks []domain.DocumentChunk, embeddings []domain.DocumentChunkEmbedding) (domain.Document, []domain.DocumentChunk, []domain.DocumentChunkEmbedding, error) {
	if len(chunks) != len(embeddings) {
		return domain.Document{}, nil, nil, errors.New("document chunks and embeddings length mismatch")
	}
	// PostgreSQL timestamps have microsecond precision. Normalize before returning
	// so the initial write and an idempotent read-back expose identical metadata.
	now := time.Now().UTC().Truncate(time.Microsecond)
	document.WorkspaceID = NormalizeWorkspaceID(document.WorkspaceID)
	document.ID = strings.TrimSpace(document.ID)
	if document.ID == "" {
		document.ID = NewID("doc")
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
	} else {
		document.CreatedAt = document.CreatedAt.UTC().Truncate(time.Microsecond)
	}
	document.UpdatedAt = now

	nonEmptyEmbeddings := 0
	for i := range chunks {
		chunks[i].ID = strings.TrimSpace(chunks[i].ID)
		if chunks[i].ID == "" {
			chunks[i].ID = NewID("chunk")
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
		} else {
			chunks[i].CreatedAt = chunks[i].CreatedAt.UTC().Truncate(time.Microsecond)
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
		} else {
			embeddings[i].CreatedAt = embeddings[i].CreatedAt.UTC().Truncate(time.Microsecond)
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

func BindDocumentID(document *domain.Document, chunks []domain.DocumentChunk, embeddings []domain.DocumentChunkEmbedding, id string) {
	document.ID = id
	for index := range chunks {
		chunks[index].DocumentID = id
		embeddings[index].ChunkID = chunks[index].ID
	}
}

func isZeroDocumentIndexIdentity(identity domain.DocumentIndexIdentity) bool {
	return identity == (domain.DocumentIndexIdentity{})
}

func DocumentVersionConflict(existing, incoming domain.Document) error {
	return fmt.Errorf("%w: source_key=%q version=%q", ErrDocumentVersionConflict, incoming.SourceKey, incoming.Version)
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
