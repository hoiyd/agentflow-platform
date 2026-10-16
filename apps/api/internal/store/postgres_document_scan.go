package store

import (
	"agentflow-platform/apps/api/internal/domain"
	"bytes"
	"database/sql"
	"encoding/json"
)

func scanDocument(row scanner) (domain.Document, error) {
	var document domain.Document
	var workspaceID sql.NullString
	var sourceURI sql.NullString
	var mimeType sql.NullString
	var metadataJSON []byte
	if err := row.Scan(
		&document.ID,
		&workspaceID,
		&document.SourceKey,
		&document.Title,
		&document.Version,
		&document.ContentHash,
		&document.IndexIdentity.ChunkerVersion,
		&document.IndexIdentity.EmbeddingProvider,
		&document.IndexIdentity.EmbeddingModel,
		&document.IndexIdentity.EmbeddingDimensions,
		&document.SourceType,
		&sourceURI,
		&mimeType,
		&metadataJSON,
		&document.CreatedAt,
		&document.UpdatedAt,
		&document.ChunkCount,
		&document.EmbeddingCount,
	); err != nil {
		return domain.Document{}, err
	}
	if workspaceID.Valid {
		document.WorkspaceID = workspaceID.String
	}
	if sourceURI.Valid {
		document.SourceURI = sourceURI.String
	}
	if mimeType.Valid {
		document.MimeType = mimeType.String
	}
	if len(metadataJSON) > 0 {
		if err := decodeDocumentMetadata(metadataJSON, &document.Metadata); err != nil {
			return domain.Document{}, err
		}
	}
	if document.Metadata == nil {
		document.Metadata = map[string]any{}
	}
	return document, nil
}

func scanDocumentChunk(row scanner) (domain.DocumentChunk, error) {
	var chunk domain.DocumentChunk
	var metadataJSON []byte
	var sectionPathJSON []byte
	if err := row.Scan(
		&chunk.ID,
		&chunk.DocumentID,
		&chunk.ParentID,
		&sectionPathJSON,
		&chunk.StartOffset,
		&chunk.EndOffset,
		&chunk.DocumentVersion,
		&chunk.ContentHash,
		&chunk.ChunkIndex,
		&chunk.Content,
		&chunk.TokenCount,
		&metadataJSON,
		&chunk.CreatedAt,
	); err != nil {
		return domain.DocumentChunk{}, err
	}
	if len(sectionPathJSON) > 0 {
		if err := json.Unmarshal(sectionPathJSON, &chunk.SectionPath); err != nil {
			return domain.DocumentChunk{}, err
		}
	}
	if len(metadataJSON) > 0 {
		if err := decodeDocumentMetadata(metadataJSON, &chunk.Metadata); err != nil {
			return domain.DocumentChunk{}, err
		}
	}
	if chunk.Metadata == nil {
		chunk.Metadata = map[string]any{}
	}
	return chunk, nil
}

func scanRetrievedDocumentChunk(row scanner) (domain.RetrievedDocumentChunk, error) {
	var item domain.RetrievedDocumentChunk
	var workspaceID sql.NullString
	var sourceURI sql.NullString
	var mimeType sql.NullString
	var documentMetadataJSON []byte
	var chunkMetadataJSON []byte
	var sectionPathJSON []byte
	if err := row.Scan(
		&item.Document.ID,
		&workspaceID,
		&item.Document.SourceKey,
		&item.Document.Title,
		&item.Document.Version,
		&item.Document.ContentHash,
		&item.Document.IndexIdentity.ChunkerVersion,
		&item.Document.IndexIdentity.EmbeddingProvider,
		&item.Document.IndexIdentity.EmbeddingModel,
		&item.Document.IndexIdentity.EmbeddingDimensions,
		&item.Document.SourceType,
		&sourceURI,
		&mimeType,
		&documentMetadataJSON,
		&item.Document.CreatedAt,
		&item.Document.UpdatedAt,
		&item.Chunk.ID,
		&item.Chunk.DocumentID,
		&item.Chunk.ParentID,
		&sectionPathJSON,
		&item.Chunk.StartOffset,
		&item.Chunk.EndOffset,
		&item.Chunk.DocumentVersion,
		&item.Chunk.ContentHash,
		&item.Chunk.ChunkIndex,
		&item.Chunk.Content,
		&item.Chunk.TokenCount,
		&chunkMetadataJSON,
		&item.Chunk.CreatedAt,
		&item.Similarity,
		&item.RecencyBoost,
		&item.Score,
	); err != nil {
		return domain.RetrievedDocumentChunk{}, err
	}
	if workspaceID.Valid {
		item.Document.WorkspaceID = workspaceID.String
	}
	if sourceURI.Valid {
		item.Document.SourceURI = sourceURI.String
	}
	if mimeType.Valid {
		item.Document.MimeType = mimeType.String
	}
	if len(documentMetadataJSON) > 0 {
		if err := decodeDocumentMetadata(documentMetadataJSON, &item.Document.Metadata); err != nil {
			return domain.RetrievedDocumentChunk{}, err
		}
	}
	if len(chunkMetadataJSON) > 0 {
		if err := decodeDocumentMetadata(chunkMetadataJSON, &item.Chunk.Metadata); err != nil {
			return domain.RetrievedDocumentChunk{}, err
		}
	}
	if len(sectionPathJSON) > 0 {
		if err := json.Unmarshal(sectionPathJSON, &item.Chunk.SectionPath); err != nil {
			return domain.RetrievedDocumentChunk{}, err
		}
	}
	if item.Document.Metadata == nil {
		item.Document.Metadata = map[string]any{}
	}
	if item.Chunk.Metadata == nil {
		item.Chunk.Metadata = map[string]any{}
	}
	item.Chunk.Document = item.Document
	return item, nil
}

func decodeDocumentMetadata(data []byte, target *map[string]any) error {
	// float64 loses integer precision and decimal scale used by SQL ->> filters.
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(target)
}
