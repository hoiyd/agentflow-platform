package store

import (
	"agentflow-platform/apps/api/internal/domain"
	"errors"
	"fmt"
	"strings"
)

func (s *PostgresStore) SearchDocumentChunks(search domain.DocumentSearch) ([]domain.RetrievedDocumentChunk, error) {
	search.WorkspaceID = NormalizeWorkspaceID(search.WorkspaceID)
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
	args = append(args, search.WorkspaceID)
	conditions = append(conditions, fmt.Sprintf("d.workspace_id = $%d", len(args)))
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
			d.id, d.workspace_id, d.source_key, d.title, d.version, d.content_hash,
			d.chunker_version, d.embedding_provider, d.embedding_model, d.embedding_dimensions,
			d.source_type, d.source_uri, d.mime_type, d.metadata, d.created_at, d.updated_at, d.metadata_schema_version,
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
	search.WorkspaceID = NormalizeWorkspaceID(search.WorkspaceID)
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
	args = append(args, search.WorkspaceID)
	conditions = append(conditions, fmt.Sprintf("d.workspace_id = $%d", len(args)))
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
			d.id, d.workspace_id, d.source_key, d.title, d.version, d.content_hash,
			d.chunker_version, d.embedding_provider, d.embedding_model, d.embedding_dimensions,
			d.source_type, d.source_uri, d.mime_type, d.metadata, d.created_at, d.updated_at, d.metadata_schema_version,
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
