package store

var postgresDocumentSchema = []string{
	`CREATE TABLE IF NOT EXISTS documents (
		id text PRIMARY KEY,
		workspace_id bigint NOT NULL,
		source_key text NOT NULL DEFAULT '',
		title text NOT NULL,
		version text NOT NULL DEFAULT '',
		content_hash text NOT NULL DEFAULT '',
		chunker_version text NOT NULL DEFAULT '',
		embedding_provider text NOT NULL DEFAULT '',
		embedding_model text NOT NULL DEFAULT '',
		embedding_dimensions integer NOT NULL DEFAULT 0,
		source_type text NOT NULL,
		source_uri text,
		mime_type text,
		content text NOT NULL,
		metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
		metadata_schema_version text NOT NULL DEFAULT '',
		created_at timestamptz NOT NULL,
		updated_at timestamptz NOT NULL
	)`,
	`ALTER TABLE documents ADD COLUMN IF NOT EXISTS version text NOT NULL DEFAULT ''`,
	`ALTER TABLE documents ADD COLUMN IF NOT EXISTS metadata_schema_version text NOT NULL DEFAULT ''`,
	`ALTER TABLE documents ADD COLUMN IF NOT EXISTS content_hash text NOT NULL DEFAULT ''`,
	`ALTER TABLE documents ADD COLUMN IF NOT EXISTS source_key text NOT NULL DEFAULT ''`,
	`UPDATE documents SET source_key = BTRIM(source_uri) WHERE source_key = '' AND OCTET_LENGTH(BTRIM(source_uri)) BETWEEN 1 AND 512`,
	`ALTER TABLE documents ADD COLUMN IF NOT EXISTS chunker_version text NOT NULL DEFAULT ''`,
	`ALTER TABLE documents ADD COLUMN IF NOT EXISTS embedding_provider text NOT NULL DEFAULT ''`,
	`ALTER TABLE documents ADD COLUMN IF NOT EXISTS embedding_model text NOT NULL DEFAULT ''`,
	`ALTER TABLE documents ADD COLUMN IF NOT EXISTS embedding_dimensions integer NOT NULL DEFAULT 0`,
	`CREATE TABLE IF NOT EXISTS document_chunks (
		id text PRIMARY KEY,
		document_id text NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
		parent_id text NOT NULL DEFAULT '',
		section_path jsonb NOT NULL DEFAULT '[]'::jsonb,
		start_offset integer NOT NULL DEFAULT 0,
		end_offset integer NOT NULL DEFAULT 0,
		document_version text NOT NULL DEFAULT '',
		content_hash text NOT NULL DEFAULT '',
		chunk_index integer NOT NULL,
		content text NOT NULL,
		token_count integer NOT NULL DEFAULT 0,
		metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
		created_at timestamptz NOT NULL
	)`,
	`ALTER TABLE document_chunks ADD COLUMN IF NOT EXISTS parent_id text NOT NULL DEFAULT ''`,
	`ALTER TABLE document_chunks ADD COLUMN IF NOT EXISTS section_path jsonb NOT NULL DEFAULT '[]'::jsonb`,
	`ALTER TABLE document_chunks ADD COLUMN IF NOT EXISTS start_offset integer NOT NULL DEFAULT 0`,
	`ALTER TABLE document_chunks ADD COLUMN IF NOT EXISTS end_offset integer NOT NULL DEFAULT 0`,
	`ALTER TABLE document_chunks ADD COLUMN IF NOT EXISTS document_version text NOT NULL DEFAULT ''`,
	`ALTER TABLE document_chunks ADD COLUMN IF NOT EXISTS content_hash text NOT NULL DEFAULT ''`,
	`CREATE TABLE IF NOT EXISTS document_chunk_embeddings (
		chunk_id text PRIMARY KEY REFERENCES document_chunks(id) ON DELETE CASCADE,
		provider text NOT NULL,
		model text NOT NULL,
		dimensions integer NOT NULL,
		embedding vector(1536) NOT NULL,
		created_at timestamptz NOT NULL
	)`,
	`DO $$
	BEGIN
		IF EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema()
				AND table_name = 'document_chunk_embeddings'
				AND column_name = 'embedding'
				AND udt_name <> 'vector'
		) THEN
			ALTER TABLE document_chunk_embeddings
				ALTER COLUMN embedding TYPE vector(1536)
				USING embedding::vector(1536);
		END IF;
	END $$`,
	`UPDATE documents d
	SET chunker_version = 'document-chunker-v1',
		embedding_provider = indexed.provider,
		embedding_model = indexed.model,
		embedding_dimensions = indexed.dimensions
	FROM (
		SELECT c.document_id, MIN(e.provider) AS provider, MIN(e.model) AS model, MIN(e.dimensions) AS dimensions
		FROM document_chunks c
		JOIN document_chunk_embeddings e ON e.chunk_id = c.id
		GROUP BY c.document_id
		HAVING COUNT(DISTINCT (e.provider, e.model, e.dimensions)) = 1
	) indexed
	WHERE d.id = indexed.document_id
		AND (d.chunker_version = '' OR d.embedding_provider = '' OR d.embedding_model = '' OR d.embedding_dimensions = 0)`,
	`WITH ranked_documents AS (
		SELECT id, ROW_NUMBER() OVER (
			PARTITION BY workspace_id, source_key
			ORDER BY updated_at DESC, created_at DESC, id DESC
		) AS source_rank
		FROM documents
		WHERE source_key <> ''
	)
	DELETE FROM documents d
	USING ranked_documents ranked
	WHERE d.id = ranked.id AND ranked.source_rank > 1`,
	`CREATE UNIQUE INDEX IF NOT EXISTS documents_workspace_source_key_idx ON documents(workspace_id, source_key) WHERE source_key <> ''`,
}

var postgresLookupIndexes = []string{
	`CREATE INDEX IF NOT EXISTS idx_memories_scope_created ON memories(workspace_id, user_id, project_id, created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_memory_candidates_conversation_created ON memory_candidates(conversation_id, created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_memory_candidates_source_message ON memory_candidates(source_message_id)`,
	`CREATE INDEX IF NOT EXISTS idx_memory_embeddings_vector ON memory_embeddings USING hnsw (embedding vector_cosine_ops)`,
	`CREATE INDEX IF NOT EXISTS idx_documents_workspace_created ON documents(workspace_id, created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_documents_metadata ON documents USING gin(metadata)`,
	`ALTER TABLE documents ADD COLUMN IF NOT EXISTS lexical_vector tsvector GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, COALESCE(title, '') || ' ' || COALESCE(source_uri, ''))) STORED`,
	`CREATE INDEX IF NOT EXISTS idx_documents_lexical_vector ON documents USING gin(lexical_vector)`,
	`CREATE INDEX IF NOT EXISTS idx_document_chunks_document ON document_chunks(document_id, chunk_index)`,
	`CREATE INDEX IF NOT EXISTS idx_document_chunks_metadata ON document_chunks USING gin(metadata)`,
	`ALTER TABLE document_chunks ADD COLUMN IF NOT EXISTS lexical_vector tsvector GENERATED ALWAYS AS (to_tsvector('simple'::regconfig, content)) STORED`,
	`CREATE INDEX IF NOT EXISTS idx_document_chunks_lexical_vector ON document_chunks USING gin(lexical_vector)`,
	`CREATE INDEX IF NOT EXISTS idx_document_chunk_embeddings_vector ON document_chunk_embeddings USING hnsw (embedding vector_cosine_ops)`,
}
