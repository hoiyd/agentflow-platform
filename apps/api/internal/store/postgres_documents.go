package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentflow-platform/apps/api/internal/domain"
)

func (s *PostgresStore) CreateDocument(document domain.Document, chunks []domain.DocumentChunk, embeddings []domain.DocumentChunkEmbedding) (domain.Document, error) {
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
	metadataJSON, err := json.Marshal(document.Metadata)
	if err != nil {
		return domain.Document{}, err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return domain.Document{}, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		INSERT INTO documents (id, workspace_id, title, version, content_hash, source_type, source_uri, mime_type, content, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		document.ID, nullString(document.WorkspaceID), document.Title, document.Version, document.ContentHash, document.SourceType, nullString(document.SourceURI), nullString(document.MimeType), document.Content, metadataJSON, document.CreatedAt, document.UpdatedAt); err != nil {
		return domain.Document{}, err
	}

	for i := range chunks {
		chunk := chunks[i]
		chunk.ID = strings.TrimSpace(chunk.ID)
		if chunk.ID == "" {
			chunk.ID = newID("chunk")
		}
		chunk.DocumentID = document.ID
		chunk.ChunkIndex = i
		chunk.Content = strings.TrimSpace(chunk.Content)
		if chunk.Content == "" {
			return domain.Document{}, errors.New("document chunk content is required")
		}
		if chunk.Metadata == nil {
			chunk.Metadata = map[string]any{}
		}
		if chunk.SectionPath == nil {
			chunk.SectionPath = []string{}
		}
		if chunk.DocumentVersion == "" {
			chunk.DocumentVersion = document.Version
		}
		if chunk.CreatedAt.IsZero() {
			chunk.CreatedAt = now
		}
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
		embedding.ChunkID = chunk.ID
		if embedding.Provider == "" {
			embedding.Provider = "local"
		}
		if embedding.Model == "" {
			embedding.Model = "local_hash"
		}
		if embedding.Dimensions == 0 {
			embedding.Dimensions = len(embedding.Embedding)
		}
		if embedding.CreatedAt.IsZero() {
			embedding.CreatedAt = now
		}
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

	document.ChunkCount = len(chunks)
	document.EmbeddingCount = len(embeddings)
	return document, tx.Commit()
}

func (s *PostgresStore) ListDocuments() ([]domain.Document, error) {
	rows, err := s.db.Query(`
		SELECT d.id, d.workspace_id, d.title, d.version, d.content_hash, d.source_type, d.source_uri, d.mime_type, d.metadata, d.created_at, d.updated_at,
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

func (s *PostgresStore) GetDocument(id string) (domain.Document, []domain.DocumentChunk, bool, error) {
	row := s.db.QueryRow(`
		SELECT d.id, d.workspace_id, d.title, d.version, d.content_hash, d.source_type, d.source_uri, d.mime_type, d.metadata, d.created_at, d.updated_at,
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

func (s *PostgresStore) SearchDocumentChunks(search domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error) {
	if len(search.Embedding) != 1536 {
		return nil, fmt.Errorf("document search embedding dimensions must be 1536, got %d", len(search.Embedding))
	}
	limit := search.Limit
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}

	args := []any{vectorLiteral(search.Embedding), limit}
	conditions := []string{}
	if strings.TrimSpace(search.WorkspaceID) != "" {
		args = append(args, search.WorkspaceID)
		conditions = append(conditions, fmt.Sprintf("d.workspace_id = $%d", len(args)))
	}
	if strings.TrimSpace(search.EmbeddingProvider) != "" {
		args = append(args, search.EmbeddingProvider)
		conditions = append(conditions, fmt.Sprintf("e.provider = $%d", len(args)))
	}
	if strings.TrimSpace(search.EmbeddingModel) != "" {
		args = append(args, search.EmbeddingModel)
		conditions = append(conditions, fmt.Sprintf("e.model = $%d", len(args)))
	}
	for key, value := range search.Metadata {
		args = append(args, key, value)
		conditions = append(conditions, fmt.Sprintf("(c.metadata ->> $%d = $%d OR d.metadata ->> $%d = $%d)", len(args)-1, len(args), len(args)-1, len(args)))
	}
	if search.MinSimilarity > 0 {
		args = append(args, search.MinSimilarity)
		conditions = append(conditions, fmt.Sprintf("(1 - (e.embedding <=> $1::vector)) >= $%d", len(args)))
	}
	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	query := `
		SELECT
			d.id, d.workspace_id, d.title, d.version, d.content_hash, d.source_type, d.source_uri, d.mime_type, d.metadata, d.created_at, d.updated_at,
			c.id, c.document_id, c.parent_id, c.section_path, c.start_offset, c.end_offset, c.document_version, c.content_hash, c.chunk_index, c.content, c.token_count, c.metadata, c.created_at,
			1 - (e.embedding <=> $1::vector) AS similarity,
			0.03 / (1 + GREATEST(EXTRACT(EPOCH FROM (now() - c.created_at)) / 86400, 0) / 30) AS recency_boost,
			(1 - (e.embedding <=> $1::vector)) + (0.03 / (1 + GREATEST(EXTRACT(EPOCH FROM (now() - c.created_at)) / 86400, 0) / 30)) AS score
		FROM document_chunks c
		JOIN documents d ON d.id = c.document_id
		JOIN document_chunk_embeddings e ON e.chunk_id = c.id
		` + where + `
		ORDER BY score DESC
		LIMIT $2`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []domain.RetrievedDocumentChunk{}
	for rows.Next() {
		item, err := scanRetrievedDocumentChunk(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) SearchDocumentChunksLexical(search domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error) {
	queryText := strings.TrimSpace(search.Query)
	if queryText == "" {
		return nil, errors.New("document lexical search query is required")
	}
	limit := search.Limit
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}

	lexicalQuery := strings.Join(search.LexicalTerms, " | ")
	args := []any{queryText, lexicalQuery, limit}
	lexicalMatch := `(
		POSITION(lower($1) IN lower(c.content)) > 0 OR
		POSITION(lower($1) IN lower(d.title)) > 0 OR
		POSITION(lower($1) IN lower(COALESCE(d.source_uri, ''))) > 0 OR
		($2 <> '' AND (c.lexical_vector @@ to_tsquery('simple', $2) OR d.lexical_vector @@ to_tsquery('simple', $2)))
	)`
	conditions := []string{lexicalMatch}
	if strings.TrimSpace(search.WorkspaceID) != "" {
		args = append(args, search.WorkspaceID)
		conditions = append(conditions, fmt.Sprintf("d.workspace_id = $%d", len(args)))
	}
	for key, value := range search.Metadata {
		args = append(args, key, value)
		conditions = append(conditions, fmt.Sprintf("(c.metadata ->> $%d = $%d OR d.metadata ->> $%d = $%d)", len(args)-1, len(args), len(args)-1, len(args)))
	}

	lexicalScore := `LEAST(1.0, (
		CASE WHEN POSITION(lower($1) IN lower(c.content)) > 0 OR POSITION(lower($1) IN lower(d.title)) > 0 OR POSITION(lower($1) IN lower(COALESCE(d.source_uri, ''))) > 0 THEN 1.0 ELSE 0.0 END +
		CASE WHEN $2 <> '' THEN 4.0 * (ts_rank_cd(c.lexical_vector, to_tsquery('simple', $2)) + ts_rank_cd(d.lexical_vector, to_tsquery('simple', $2))) ELSE 0.0 END
	))`
	recencyBoost := `(0.03 / (1 + GREATEST(EXTRACT(EPOCH FROM (now() - c.created_at)) / 86400, 0) / 30))`
	query := `
		SELECT
			d.id, d.workspace_id, d.title, d.version, d.content_hash, d.source_type, d.source_uri, d.mime_type, d.metadata, d.created_at, d.updated_at,
			c.id, c.document_id, c.parent_id, c.section_path, c.start_offset, c.end_offset, c.document_version, c.content_hash, c.chunk_index, c.content, c.token_count, c.metadata, c.created_at,
			0::double precision AS similarity,
			` + recencyBoost + ` AS recency_boost,
			` + lexicalScore + ` + ` + recencyBoost + ` AS score
		FROM document_chunks c
		JOIN documents d ON d.id = c.document_id
		WHERE ` + strings.Join(conditions, " AND ") + `
		ORDER BY ` + lexicalScore + ` DESC, c.created_at DESC
		LIMIT $3`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []domain.RetrievedDocumentChunk{}
	for rows.Next() {
		item, err := scanRetrievedDocumentChunk(rows)
		if err != nil {
			return nil, err
		}
		item.LexicalScore = item.Score - item.RecencyBoost
		if item.LexicalScore < 0 {
			item.LexicalScore = 0
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) ListDocumentContextChunks(search domain.DocumentContextSearch) ([]domain.RetrievedDocumentChunk, error) {
	documentID := strings.TrimSpace(search.DocumentID)
	if documentID == "" {
		return nil, errors.New("document context search document ID is required")
	}

	args := []any{documentID}
	conditions := []string{"d.id = $1"}
	if workspaceID := strings.TrimSpace(search.WorkspaceID); workspaceID != "" {
		args = append(args, workspaceID)
		conditions = append(conditions, fmt.Sprintf("d.workspace_id = $%d", len(args)))
	}
	for key, value := range search.Metadata {
		args = append(args, key, value)
		conditions = append(conditions, fmt.Sprintf("(c.metadata ->> $%d = $%d OR d.metadata ->> $%d = $%d)", len(args)-1, len(args), len(args)-1, len(args)))
	}

	contextConditions := make([]string, 0, 2)
	if parentID := strings.TrimSpace(search.ParentID); parentID != "" {
		args = append(args, parentID)
		contextConditions = append(contextConditions, fmt.Sprintf("c.parent_id = $%d", len(args)))
	}
	if search.NeighborWindow > 0 {
		args = append(args, search.ChunkIndex-search.NeighborWindow, search.ChunkIndex+search.NeighborWindow)
		contextConditions = append(contextConditions, fmt.Sprintf("c.chunk_index BETWEEN $%d AND $%d", len(args)-1, len(args)))
	}
	if len(contextConditions) == 0 {
		return []domain.RetrievedDocumentChunk{}, nil
	}
	conditions = append(conditions, "("+strings.Join(contextConditions, " OR ")+")")
	args = append(args, search.ChunkIndex)
	centerPlaceholder := len(args)

	query := `
		SELECT
			d.id, d.workspace_id, d.title, d.version, d.content_hash, d.source_type, d.source_uri, d.mime_type, d.metadata, d.created_at, d.updated_at,
			c.id, c.document_id, c.parent_id, c.section_path, c.start_offset, c.end_offset, c.document_version, c.content_hash, c.chunk_index, c.content, c.token_count, c.metadata, c.created_at,
			0::double precision AS similarity,
			0::double precision AS recency_boost,
			0::double precision AS score
		FROM document_chunks c
		JOIN documents d ON d.id = c.document_id
		WHERE ` + strings.Join(conditions, " AND ") + `
		ORDER BY ABS(c.chunk_index - $` + fmt.Sprint(centerPlaceholder) + `), c.chunk_index, c.id`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []domain.RetrievedDocumentChunk{}
	for rows.Next() {
		item, err := scanRetrievedDocumentChunk(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanDocument(row scanner) (domain.Document, error) {
	var document domain.Document
	var workspaceID sql.NullString
	var sourceURI sql.NullString
	var mimeType sql.NullString
	var metadataJSON []byte
	if err := row.Scan(
		&document.ID,
		&workspaceID,
		&document.Title,
		&document.Version,
		&document.ContentHash,
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
		if err := json.Unmarshal(metadataJSON, &document.Metadata); err != nil {
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
		if err := json.Unmarshal(metadataJSON, &chunk.Metadata); err != nil {
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
		&item.Document.Title,
		&item.Document.Version,
		&item.Document.ContentHash,
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
		if err := json.Unmarshal(documentMetadataJSON, &item.Document.Metadata); err != nil {
			return domain.RetrievedDocumentChunk{}, err
		}
	}
	if len(chunkMetadataJSON) > 0 {
		if err := json.Unmarshal(chunkMetadataJSON, &item.Chunk.Metadata); err != nil {
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
