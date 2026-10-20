package store

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/rag/eligibility"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func (s *PostgresStore) CreateDocument(document domain.Document, chunks []domain.DocumentChunk, embeddings []domain.DocumentChunkEmbedding) (domain.Document, error) {
	document, chunks, embeddings, err := PrepareDocumentWrite(document, chunks, embeddings)
	if err != nil {
		return domain.Document{}, err
	}
	metadataJSON, err := json.Marshal(document.Metadata)
	if err != nil {
		return domain.Document{}, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return domain.Document{}, err
	}
	defer tx.Rollback()

	if document.SourceKey != "" {
		if _, err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`, document.WorkspaceID, document.SourceKey); err != nil {
			return domain.Document{}, err
		}
		existing, found, err := getDocumentBySourceTx(tx, document.WorkspaceID, document.SourceKey)
		if err != nil {
			return domain.Document{}, err
		}
		if found {
			// A content rebuild cannot overwrite a concurrent withdrawal. Change
			// lifecycle through its conditional update before replacing the index.
			if existing.Lifecycle != document.Lifecycle {
				return domain.Document{}, ErrDocumentMetadataConflict
			}
			if existing.MetadataSchemaVersion != "" && existing.MetadataSchemaVersion != document.MetadataSchemaVersion {
				return domain.Document{}, ErrDocumentMetadataConflict
			}
			if existing.Version == document.Version && existing.ContentHash != document.ContentHash {
				return domain.Document{}, DocumentVersionConflict(existing, document)
			}
			if SameDocumentIndexInputs(existing, document) && existing.IndexIdentity == document.IndexIdentity {
				if existing.MetadataSchemaVersion != document.MetadataSchemaVersion || !SameDocumentMetadata(existing.Metadata, document.Metadata) || existing.Lifecycle != document.Lifecycle {
					return domain.Document{}, ErrDocumentMetadataConflict
				}
				return existing, nil
			}
			document.CreatedAt = existing.CreatedAt
			BindDocumentID(&document, chunks, embeddings, existing.ID)
			if _, err := tx.Exec(`DELETE FROM documents WHERE id = $1`, existing.ID); err != nil {
				return domain.Document{}, err
			}
		}
	}

	lifecycleJSON, _ := json.Marshal(document.Lifecycle)
	if _, err := tx.Exec(`
		INSERT INTO documents (id, workspace_id, source_key, title, version, content_hash, chunker_version, embedding_provider, embedding_model, embedding_dimensions, source_type, source_uri, mime_type, content, metadata, created_at, updated_at, metadata_schema_version, lifecycle)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)`,
		document.ID, document.WorkspaceID, document.SourceKey, document.Title, document.Version, document.ContentHash,
		document.IndexIdentity.ChunkerVersion, document.IndexIdentity.EmbeddingProvider, document.IndexIdentity.EmbeddingModel, document.IndexIdentity.EmbeddingDimensions,
		document.SourceType, nullString(document.SourceURI), nullString(document.MimeType), document.Content, metadataJSON, document.CreatedAt, document.UpdatedAt, document.MetadataSchemaVersion, lifecycleJSON); err != nil {
		return domain.Document{}, err
	}

	for i := range chunks {
		chunk := chunks[i]
		chunkMetadataJSON, err := json.Marshal(chunk.Metadata)
		if err != nil {
			return domain.Document{}, err
		}
		sectionPathJSON, err := json.Marshal(chunk.SectionPath)
		if err != nil {
			return domain.Document{}, err
		}
		if _, err := tx.Exec(`
			INSERT INTO document_chunks (id, document_id, parent_id, section_path, start_offset, end_offset, document_version, content_hash, chunk_index, content, token_count, metadata, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
			chunk.ID, chunk.DocumentID, chunk.ParentID, sectionPathJSON, chunk.StartOffset, chunk.EndOffset, chunk.DocumentVersion, chunk.ContentHash, chunk.ChunkIndex, chunk.Content, chunk.TokenCount, chunkMetadataJSON, chunk.CreatedAt); err != nil {
			return domain.Document{}, err
		}

		embedding := embeddings[i]
		if len(embedding.Embedding) != 1536 {
			return domain.Document{}, fmt.Errorf("document chunk embedding dimensions must be 1536, got %d", len(embedding.Embedding))
		}
		if _, err := tx.Exec(`
			INSERT INTO document_chunk_embeddings (chunk_id, provider, model, dimensions, embedding, created_at)
			VALUES ($1, $2, $3, $4, $5::vector, $6)`,
			embedding.ChunkID, embedding.Provider, embedding.Model, embedding.Dimensions, vectorLiteral(embedding.Embedding), embedding.CreatedAt); err != nil {
			return domain.Document{}, err
		}
	}

	return document, tx.Commit()
}

func getDocumentBySourceTx(tx *sql.Tx, workspaceID, sourceKey string) (domain.Document, bool, error) {
	var id string
	if err := tx.QueryRow(`SELECT id FROM documents WHERE workspace_id=$1 AND source_key=$2 FOR UPDATE`, workspaceID, sourceKey).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.Document{}, false, nil
		}
		return domain.Document{}, false, err
	}
	return getDocumentBySource(tx, workspaceID, sourceKey)
}

type documentQueryer interface{ QueryRow(string, ...any) *sql.Row }

func getDocumentBySource(db documentQueryer, workspaceID, sourceKey string) (domain.Document, bool, error) {
	row := db.QueryRow(`
		SELECT d.id, d.workspace_id, d.source_key, d.title, d.version, d.content_hash,
			d.chunker_version, d.embedding_provider, d.embedding_model, d.embedding_dimensions,
			d.source_type, d.source_uri, d.mime_type, d.metadata, d.created_at, d.updated_at, d.metadata_schema_version, d.lifecycle,
			COUNT(DISTINCT c.id), COUNT(e.chunk_id)
		FROM documents d
		LEFT JOIN document_chunks c ON c.document_id = d.id
		LEFT JOIN document_chunk_embeddings e ON e.chunk_id = c.id
		WHERE d.workspace_id = $1 AND d.source_key = $2
		GROUP BY d.id`, workspaceID, sourceKey)
	document, err := scanDocument(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Document{}, false, nil
	}
	return document, err == nil, err
}

func (s *PostgresStore) ListDocuments() ([]domain.Document, error) {
	rows, err := s.db.Query(`
		SELECT d.id, d.workspace_id, d.source_key, d.title, d.version, d.content_hash,
			d.chunker_version, d.embedding_provider, d.embedding_model, d.embedding_dimensions,
			d.source_type, d.source_uri, d.mime_type, d.metadata, d.created_at, d.updated_at, d.metadata_schema_version, d.lifecycle,
			COUNT(DISTINCT c.id) AS chunk_count,
			COUNT(e.chunk_id) AS embedding_count
		FROM documents d
		LEFT JOIN document_chunks c ON c.document_id = d.id
		LEFT JOIN document_chunk_embeddings e ON e.chunk_id = c.id
		GROUP BY d.id
		ORDER BY d.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []domain.Document{}
	for rows.Next() {
		document, err := scanDocument(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, document)
	}
	return items, rows.Err()
}

func (s *PostgresStore) ListDocumentsByWorkspace(workspaceID string) ([]domain.Document, error) {
	rows, err := s.db.Query(`
		SELECT d.id, d.workspace_id, d.source_key, d.title, d.version, d.content_hash,
			d.chunker_version, d.embedding_provider, d.embedding_model, d.embedding_dimensions,
			d.source_type, d.source_uri, d.mime_type, d.metadata, d.created_at, d.updated_at, d.metadata_schema_version, d.lifecycle,
			COUNT(DISTINCT c.id), COUNT(e.chunk_id)
		FROM documents d
		LEFT JOIN document_chunks c ON c.document_id = d.id
		LEFT JOIN document_chunk_embeddings e ON e.chunk_id = c.id
		WHERE d.workspace_id = $1 GROUP BY d.id ORDER BY d.created_at DESC`, NormalizeWorkspaceID(workspaceID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.Document{}
	for rows.Next() {
		document, scanErr := scanDocument(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, document)
	}
	return items, rows.Err()
}

func (s *PostgresStore) ListDocumentIndexIdentities(workspaceID string) ([]domain.DocumentIndexIdentity, error) {
	predicate, _ := eligibility.Compile(nil)
	args := []any{NormalizeWorkspaceID(workspaceID)}
	where := predicate.SQL(&args)
	rows, err := s.db.Query(`
		SELECT DISTINCT chunker_version, embedding_provider, embedding_model, embedding_dimensions
		FROM documents d
		WHERE workspace_id = $1 AND `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []domain.DocumentIndexIdentity{}
	for rows.Next() {
		var identity domain.DocumentIndexIdentity
		if err := rows.Scan(&identity.ChunkerVersion, &identity.EmbeddingProvider, &identity.EmbeddingModel, &identity.EmbeddingDimensions); err != nil {
			return nil, err
		}
		items = append(items, identity)
	}
	return items, rows.Err()
}

func (s *PostgresStore) GetDocument(id string) (domain.Document, []domain.DocumentChunk, bool, error) {
	row := s.db.QueryRow(`
		SELECT d.id, d.workspace_id, d.source_key, d.title, d.version, d.content_hash,
			d.chunker_version, d.embedding_provider, d.embedding_model, d.embedding_dimensions,
			d.source_type, d.source_uri, d.mime_type, d.metadata, d.created_at, d.updated_at, d.metadata_schema_version, d.lifecycle,
			COUNT(DISTINCT c.id) AS chunk_count,
			COUNT(e.chunk_id) AS embedding_count
		FROM documents d
		LEFT JOIN document_chunks c ON c.document_id = d.id
		LEFT JOIN document_chunk_embeddings e ON e.chunk_id = c.id
		WHERE d.id = $1
		GROUP BY d.id`, strings.TrimSpace(id))
	document, err := scanDocument(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Document{}, nil, false, nil
	}
	if err != nil {
		return domain.Document{}, nil, false, err
	}

	rows, err := s.db.Query(`
		SELECT id, document_id, parent_id, section_path, start_offset, end_offset, document_version, content_hash, chunk_index, content, token_count, metadata, created_at
		FROM document_chunks
		WHERE document_id = $1
		ORDER BY chunk_index ASC`, document.ID)
	if err != nil {
		return domain.Document{}, nil, false, err
	}
	defer rows.Close()

	chunks := []domain.DocumentChunk{}
	for rows.Next() {
		chunk, err := scanDocumentChunk(rows)
		if err != nil {
			return domain.Document{}, nil, false, err
		}
		chunk.Document = document
		chunks = append(chunks, chunk)
	}
	return document, chunks, true, rows.Err()
}

func (s *PostgresStore) GetDocumentInWorkspace(workspaceID string, id string) (domain.Document, []domain.DocumentChunk, bool, error) {
	row := s.db.QueryRow(`
		SELECT d.id, d.workspace_id, d.source_key, d.title, d.version, d.content_hash,
			d.chunker_version, d.embedding_provider, d.embedding_model, d.embedding_dimensions,
			d.source_type, d.source_uri, d.mime_type, d.metadata, d.created_at, d.updated_at, d.metadata_schema_version, d.lifecycle,
			COUNT(DISTINCT c.id) AS chunk_count,
			COUNT(e.chunk_id) AS embedding_count
		FROM documents d
		LEFT JOIN document_chunks c ON c.document_id = d.id
		LEFT JOIN document_chunk_embeddings e ON e.chunk_id = c.id
		WHERE d.id = $1 AND d.workspace_id = $2
		GROUP BY d.id`, strings.TrimSpace(id), NormalizeWorkspaceID(workspaceID))
	document, err := scanDocument(row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Document{}, nil, false, nil
	}
	if err != nil {
		return domain.Document{}, nil, false, err
	}

	rows, err := s.db.Query(`
		SELECT c.id, c.document_id, c.parent_id, c.section_path, c.start_offset, c.end_offset, c.document_version, c.content_hash, c.chunk_index, c.content, c.token_count, c.metadata, c.created_at
		FROM document_chunks c
		JOIN documents d ON d.id = c.document_id
		WHERE c.document_id = $1 AND d.workspace_id = $2
		ORDER BY c.chunk_index ASC`, document.ID, document.WorkspaceID)
	if err != nil {
		return domain.Document{}, nil, false, err
	}
	defer rows.Close()
	chunks := []domain.DocumentChunk{}
	for rows.Next() {
		chunk, scanErr := scanDocumentChunk(rows)
		if scanErr != nil {
			return domain.Document{}, nil, false, scanErr
		}
		chunk.Document = document
		chunks = append(chunks, chunk)
	}
	return document, chunks, true, rows.Err()
}

func (s *PostgresStore) DeleteDocument(id string) error {
	result, err := s.db.Exec(`DELETE FROM documents WHERE id = $1`, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if deleted == 0 {
		return ErrNotFound("document")
	}
	return nil
}

func (s *PostgresStore) DeleteDocumentInWorkspace(workspaceID string, id string) error {
	result, err := s.db.Exec(`DELETE FROM documents WHERE id = $1 AND workspace_id = $2`, strings.TrimSpace(id), NormalizeWorkspaceID(workspaceID))
	if err != nil {
		return err
	}
	if deleted, affectedErr := result.RowsAffected(); affectedErr == nil && deleted == 0 {
		return ErrNotFound("document")
	}
	return nil
}
