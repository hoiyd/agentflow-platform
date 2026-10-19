package store

import (
	"agentflow-platform/apps/api/internal/domain"
	"agentflow-platform/apps/api/internal/rag/eligibility"
	"errors"
	"fmt"
	"strings"
)

func (s *PostgresStore) ListDocumentContextChunks(search domain.DocumentContextSearch) ([]domain.RetrievedDocumentChunk, error) {
	predicate, err := eligibility.Compile(search.MetadataFilter)
	if err != nil {
		return nil, err
	}
	search.WorkspaceID = NormalizeWorkspaceID(search.WorkspaceID)
	documentID := strings.TrimSpace(search.DocumentID)
	if documentID == "" {
		return nil, errors.New("document context search document ID is required")
	}

	args := []any{documentID}
	conditions := []string{"d.id = $1"}
	args = append(args, search.WorkspaceID)
	conditions = append(conditions, fmt.Sprintf("d.workspace_id = $%d", len(args)))
	for key, value := range search.Metadata {
		args = append(args, key, value)
		conditions = append(conditions, fmt.Sprintf("(c.metadata ->> $%d = $%d OR d.metadata ->> $%d = $%d)", len(args)-1, len(args), len(args)-1, len(args)))
	}
	conditions = append(conditions, predicate.SQL(&args))

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
			d.id, d.workspace_id, d.source_key, d.title, d.version, d.content_hash,
			d.chunker_version, d.embedding_provider, d.embedding_model, d.embedding_dimensions,
			d.source_type, d.source_uri, d.mime_type, d.metadata, d.created_at, d.updated_at, d.metadata_schema_version, d.lifecycle,
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
