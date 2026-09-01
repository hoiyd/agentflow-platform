package store

import "context"

func (s *PostgresStore) migrate(ctx context.Context) error {
	for _, statement := range postgresMigrations {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

var postgresMigrations = []string{
	`CREATE EXTENSION IF NOT EXISTS vector`,
	`CREATE TABLE IF NOT EXISTS conversations (
		id text PRIMARY KEY,
		workspace_id text NOT NULL DEFAULT 'default_workspace',
		user_id text,
		project_id text,
		title text NOT NULL,
		created_at timestamptz NOT NULL,
		updated_at timestamptz NOT NULL
	)`,
	`ALTER TABLE conversations ADD COLUMN IF NOT EXISTS workspace_id text`,
	`UPDATE conversations SET workspace_id = 'default_workspace' WHERE workspace_id IS NULL OR BTRIM(workspace_id) = '' OR workspace_id = 'default'`,
	`ALTER TABLE conversations ALTER COLUMN workspace_id SET DEFAULT 'default_workspace'`,
	`ALTER TABLE conversations ALTER COLUMN workspace_id SET NOT NULL`,
	`CREATE INDEX IF NOT EXISTS conversations_workspace_updated_idx ON conversations(workspace_id, updated_at DESC)`,
	`CREATE TABLE IF NOT EXISTS agents (
		id text PRIMARY KEY,
		workspace_id text,
		user_id text,
		project_id text,
		name text NOT NULL,
		description text NOT NULL DEFAULT '',
		system_prompt text NOT NULL DEFAULT '',
		tools jsonb NOT NULL DEFAULT '[]'::jsonb,
		memory_enabled boolean NOT NULL DEFAULT true,
		retrieval_enabled boolean NOT NULL DEFAULT true,
		executor text NOT NULL DEFAULT 'native',
		deleted_at timestamptz,
		created_at timestamptz NOT NULL,
		updated_at timestamptz NOT NULL
	)`,
	`ALTER TABLE agents ADD COLUMN IF NOT EXISTS memory_enabled boolean NOT NULL DEFAULT true`,
	`ALTER TABLE agents ADD COLUMN IF NOT EXISTS retrieval_enabled boolean NOT NULL DEFAULT true`,
	`ALTER TABLE agents ADD COLUMN IF NOT EXISTS executor text NOT NULL DEFAULT 'native'`,
	`ALTER TABLE agents ADD COLUMN IF NOT EXISTS deleted_at timestamptz`,
	`DO $$
	BEGIN
		IF EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_name = 'agents' AND column_name = 'archived'
		) THEN
			UPDATE agents SET deleted_at = COALESCE(deleted_at, updated_at) WHERE archived = true;
		END IF;
	END $$`,
	`ALTER TABLE agents DROP COLUMN IF EXISTS archived`,
	`CREATE TABLE IF NOT EXISTS messages (
		id text PRIMARY KEY,
		conversation_id text NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
		workspace_id text NOT NULL DEFAULT 'default_workspace',
		user_id text,
		project_id text,
		role text NOT NULL,
		content text NOT NULL,
		citations jsonb NOT NULL DEFAULT '[]'::jsonb,
		created_at timestamptz NOT NULL
	)`,
	`ALTER TABLE messages ADD COLUMN IF NOT EXISTS workspace_id text`,
	`UPDATE messages m SET workspace_id = c.workspace_id FROM conversations c WHERE c.id = m.conversation_id AND (m.workspace_id IS NULL OR BTRIM(m.workspace_id) = '' OR m.workspace_id = 'default')`,
	`UPDATE messages SET workspace_id = 'default_workspace' WHERE workspace_id IS NULL OR BTRIM(workspace_id) = '' OR workspace_id = 'default'`,
	`ALTER TABLE messages ALTER COLUMN workspace_id SET DEFAULT 'default_workspace'`,
	`ALTER TABLE messages ALTER COLUMN workspace_id SET NOT NULL`,
	`ALTER TABLE messages ADD COLUMN IF NOT EXISTS citations jsonb NOT NULL DEFAULT '[]'::jsonb`,
	`CREATE TABLE IF NOT EXISTS runs (
		id text PRIMARY KEY,
		agent_id text NOT NULL REFERENCES agents(id),
		conversation_id text NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
		workspace_id text NOT NULL DEFAULT 'default_workspace',
		user_id text,
		project_id text,
		status text NOT NULL,
		error text NOT NULL DEFAULT '',
		started_at timestamptz,
		heartbeat_at timestamptz,
		completed_at timestamptz,
		created_at timestamptz NOT NULL,
		updated_at timestamptz NOT NULL
	)`,
	`ALTER TABLE runs ADD COLUMN IF NOT EXISTS workspace_id text`,
	`UPDATE runs r SET workspace_id = c.workspace_id FROM conversations c WHERE c.id = r.conversation_id AND (r.workspace_id IS NULL OR BTRIM(r.workspace_id) = '' OR r.workspace_id = 'default')`,
	`UPDATE runs SET workspace_id = 'default_workspace' WHERE workspace_id IS NULL OR BTRIM(workspace_id) = '' OR workspace_id = 'default'`,
	`ALTER TABLE runs ALTER COLUMN workspace_id SET DEFAULT 'default_workspace'`,
	`ALTER TABLE runs ALTER COLUMN workspace_id SET NOT NULL`,
	`CREATE INDEX IF NOT EXISTS runs_workspace_created_idx ON runs(workspace_id, created_at DESC)`,
	`ALTER TABLE runs ADD COLUMN IF NOT EXISTS heartbeat_at timestamptz`,
	`ALTER TABLE runs ADD COLUMN IF NOT EXISTS runtime_snapshot jsonb`,
	`ALTER TABLE runs ADD COLUMN IF NOT EXISTS completion_contract jsonb`,
	`ALTER TABLE runs ADD COLUMN IF NOT EXISTS verification_status text NOT NULL DEFAULT 'not_required'`,
	`ALTER TABLE runs ADD COLUMN IF NOT EXISTS execution_started_at timestamptz`,
	`ALTER TABLE runs ADD COLUMN IF NOT EXISTS active_runtime_ms bigint NOT NULL DEFAULT 0`,
	`CREATE TABLE IF NOT EXISTS run_delegations (
		id text PRIMARY KEY,
		workspace_id text NOT NULL,
		conversation_id text NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
		parent_run_id text NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
		parent_turn_id text NOT NULL,
		parent_stage_id text NOT NULL DEFAULT '',
		child_run_id text NOT NULL UNIQUE REFERENCES runs(id) ON DELETE CASCADE,
		agent_id text NOT NULL REFERENCES agents(id),
		depth integer NOT NULL,
		status text NOT NULL,
		block_reason text NOT NULL DEFAULT '',
		task text NOT NULL,
		summary text NOT NULL DEFAULT '',
		output_ref text NOT NULL DEFAULT '',
		output_hash text NOT NULL DEFAULT '',
		output_bytes integer NOT NULL DEFAULT 0,
		summary_truncated boolean NOT NULL DEFAULT false,
		timeout_ms bigint NOT NULL DEFAULT 0,
		error text NOT NULL DEFAULT '',
		created_at timestamptz NOT NULL,
		updated_at timestamptz NOT NULL
	)`,
	`ALTER TABLE run_delegations ADD COLUMN IF NOT EXISTS block_reason text NOT NULL DEFAULT ''`,
	`CREATE TABLE IF NOT EXISTS collaboration_steps (
		id text PRIMARY KEY,
		run_id text NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
		conversation_id text NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
		workspace_id text,
		user_id text,
		project_id text,
		role text NOT NULL,
		agent_id text NOT NULL DEFAULT '',
		status text NOT NULL,
		iteration integer NOT NULL DEFAULT 0,
		input text NOT NULL DEFAULT '',
		output text NOT NULL DEFAULT '',
		error text NOT NULL DEFAULT '',
		created_at timestamptz NOT NULL,
		updated_at timestamptz NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS run_events (
		id text PRIMARY KEY,
		run_id text NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
		conversation_id text,
		stage_id text,
		turn_id text,
		parent_event_id text,
		type text NOT NULL,
		schema_version integer NOT NULL,
		sequence bigint NOT NULL,
		payload jsonb NOT NULL DEFAULT '{}'::jsonb,
		timestamp timestamptz NOT NULL,
		UNIQUE(run_id, sequence)
	)`,
	`CREATE TABLE IF NOT EXISTS stage_checkpoints (
		id text PRIMARY KEY,
		provider text NOT NULL,
		run_id text NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
		conversation_id text NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
		stage_id text NOT NULL,
		status text NOT NULL,
		input_hash text NOT NULL,
		output_hash text NOT NULL DEFAULT '',
		runtime_snapshot_hash text NOT NULL,
		tool_definitions_hash text NOT NULL,
		event_cursor bigint NOT NULL,
		error text NOT NULL DEFAULT '',
		created_at timestamptz NOT NULL,
		updated_at timestamptz NOT NULL,
		UNIQUE(run_id, stage_id)
	)`,
	`CREATE INDEX IF NOT EXISTS stage_checkpoints_run_cursor_idx ON stage_checkpoints(run_id, event_cursor)`,
	`CREATE TABLE IF NOT EXISTS tool_effects (
		idempotency_key text PRIMARY KEY,
		run_id text NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
		stage_id text NOT NULL,
		turn_id text NOT NULL DEFAULT '',
		tool_call_id text NOT NULL,
		tool_name text NOT NULL,
		request_hash text NOT NULL,
		status text NOT NULL,
		result bytea,
		error text NOT NULL DEFAULT '',
		created_at timestamptz NOT NULL,
		updated_at timestamptz NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS tool_effects_run_stage_idx ON tool_effects(run_id, stage_id, created_at)`,
	`CREATE TABLE IF NOT EXISTS tool_artifacts (
		id text PRIMARY KEY,
		schema_version integer NOT NULL,
		run_id text NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
		stage_id text NOT NULL DEFAULT '',
		turn_id text NOT NULL DEFAULT '',
		tool_call_id text NOT NULL,
		tool_name text NOT NULL,
		definition_revision text NOT NULL DEFAULT '',
		media_type text NOT NULL,
		content_hash text NOT NULL,
		original_byte_size integer NOT NULL,
		stored_byte_size integer NOT NULL,
		redacted boolean NOT NULL DEFAULT false,
		redaction_strategy text NOT NULL DEFAULT '',
		redaction_count integer NOT NULL DEFAULT 0,
		content bytea NOT NULL,
		created_at timestamptz NOT NULL,
		expires_at timestamptz
	)`,
	`CREATE INDEX IF NOT EXISTS tool_artifacts_run_created_idx ON tool_artifacts(run_id, created_at, id)`,
	`CREATE INDEX IF NOT EXISTS tool_artifacts_call_idx ON tool_artifacts(run_id, tool_call_id)`,
	`CREATE TABLE IF NOT EXISTS verification_evidence (
		id text PRIMARY KEY,
		run_id text NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
		stage_id text,
		contract_id text NOT NULL,
		contract_version integer NOT NULL,
		verifier_id text NOT NULL,
		verifier_type text NOT NULL,
		verifier_version text NOT NULL,
		attempt integer NOT NULL,
		subject_hash text NOT NULL,
		snapshot_hash text NOT NULL,
		status text NOT NULL,
		started_at timestamptz NOT NULL,
		completed_at timestamptz NOT NULL,
		duration_ms bigint NOT NULL,
		exit_code integer,
		summary text NOT NULL DEFAULT '',
		details jsonb NOT NULL DEFAULT '{}'::jsonb,
		artifact_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
		supersedes_evidence_id text
	)`,
	`ALTER TABLE verification_evidence ADD COLUMN IF NOT EXISTS details jsonb NOT NULL DEFAULT '{}'::jsonb`,
	`CREATE INDEX IF NOT EXISTS verification_evidence_run_idx ON verification_evidence(run_id, started_at)`,
	`CREATE TABLE IF NOT EXISTS verification_artifacts (
		id text PRIMARY KEY,
		run_id text NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
		evidence_id text NOT NULL REFERENCES verification_evidence(id) ON DELETE CASCADE,
		kind text NOT NULL,
		media_type text NOT NULL,
		content text NOT NULL,
		content_hash text NOT NULL,
		byte_size integer NOT NULL,
		truncated boolean NOT NULL DEFAULT false,
		created_at timestamptz NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS verification_artifacts_run_idx ON verification_artifacts(run_id, created_at)`,
	`CREATE TABLE IF NOT EXISTS context_compactions (
		id text PRIMARY KEY,
		conversation_id text NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
		run_id text NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
			trigger text NOT NULL,
			status text NOT NULL DEFAULT 'completed',
			generation bigint NOT NULL DEFAULT 1,
			previous_compaction_id text NOT NULL DEFAULT '',
			replacement_summary_id text NOT NULL DEFAULT '',
			summary text NOT NULL,
			source_message_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
			source_event_ids jsonb NOT NULL DEFAULT '[]'::jsonb,
			shadowed_first_message_id text NOT NULL DEFAULT '',
			shadowed_last_message_id text NOT NULL DEFAULT '',
			shadowed_message_count integer NOT NULL DEFAULT 0,
			source_hash text NOT NULL,
			before_tokens integer NOT NULL,
			after_tokens integer NOT NULL,
			target_summary_tokens integer NOT NULL DEFAULT 0,
			reduction_ratio double precision NOT NULL DEFAULT 0,
			consecutive_low_yield integer NOT NULL DEFAULT 0,
			summary_model text NOT NULL,
			algorithm_version text NOT NULL,
			surface_replaced_at timestamptz,
			created_at timestamptz NOT NULL,
			UNIQUE(conversation_id, source_hash)
		)`,
	`ALTER TABLE context_compactions ADD COLUMN IF NOT EXISTS status text NOT NULL DEFAULT 'completed'`,
	`ALTER TABLE context_compactions ADD COLUMN IF NOT EXISTS generation bigint NOT NULL DEFAULT 1`,
	`ALTER TABLE context_compactions ADD COLUMN IF NOT EXISTS previous_compaction_id text NOT NULL DEFAULT ''`,
	`ALTER TABLE context_compactions ADD COLUMN IF NOT EXISTS replacement_summary_id text NOT NULL DEFAULT ''`,
	`ALTER TABLE context_compactions ADD COLUMN IF NOT EXISTS source_event_ids jsonb NOT NULL DEFAULT '[]'::jsonb`,
	`ALTER TABLE context_compactions ADD COLUMN IF NOT EXISTS shadowed_first_message_id text NOT NULL DEFAULT ''`,
	`ALTER TABLE context_compactions ADD COLUMN IF NOT EXISTS shadowed_last_message_id text NOT NULL DEFAULT ''`,
	`ALTER TABLE context_compactions ADD COLUMN IF NOT EXISTS shadowed_message_count integer NOT NULL DEFAULT 0`,
	`ALTER TABLE context_compactions ADD COLUMN IF NOT EXISTS target_summary_tokens integer NOT NULL DEFAULT 0`,
	`ALTER TABLE context_compactions ADD COLUMN IF NOT EXISTS reduction_ratio double precision NOT NULL DEFAULT 0`,
	`ALTER TABLE context_compactions ADD COLUMN IF NOT EXISTS consecutive_low_yield integer NOT NULL DEFAULT 0`,
	`ALTER TABLE context_compactions ADD COLUMN IF NOT EXISTS surface_replaced_at timestamptz`,
	`UPDATE context_compactions SET replacement_summary_id = 'summary:' || id WHERE replacement_summary_id = ''`,
	`DO $$ BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_indexes WHERE indexname = 'context_compactions_generation_idx') THEN
				WITH ranked AS (
					SELECT id, ROW_NUMBER() OVER (PARTITION BY conversation_id ORDER BY created_at, id) AS generation
					FROM context_compactions
				) UPDATE context_compactions SET generation = ranked.generation FROM ranked WHERE context_compactions.id = ranked.id;
			END IF;
		END $$`,
	`WITH ordered AS (
			SELECT id, LAG(id) OVER (PARTITION BY conversation_id ORDER BY generation, created_at, id) AS previous_id
			FROM context_compactions
		) UPDATE context_compactions
		SET previous_compaction_id = COALESCE(ordered.previous_id, '')
		FROM ordered
		WHERE context_compactions.id = ordered.id AND context_compactions.previous_compaction_id = ''`,
	`UPDATE context_compactions
		SET shadowed_first_message_id = COALESCE(source_message_ids->>0, ''),
			shadowed_last_message_id = COALESCE(source_message_ids->>(jsonb_array_length(source_message_ids)-1), ''),
			shadowed_message_count = jsonb_array_length(source_message_ids)
		WHERE shadowed_message_count = 0 AND jsonb_array_length(source_message_ids) > 0`,
	`CREATE UNIQUE INDEX IF NOT EXISTS context_compactions_generation_idx ON context_compactions(conversation_id, generation)`,
	`CREATE INDEX IF NOT EXISTS context_compactions_active_idx ON context_compactions(conversation_id, generation DESC) WHERE status = 'completed'`,
	`CREATE TABLE IF NOT EXISTS model_request_records (
		id text PRIMARY KEY,
		run_id text NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
		conversation_id text NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
		stage_id text,
		turn_id text,
		model_call_id text NOT NULL,
		attempt integer NOT NULL,
		operation text NOT NULL,
		provider text NOT NULL,
		model text NOT NULL,
		context_manifest_id text,
		runtime_snapshot_hash text NOT NULL,
		payload_hash text NOT NULL,
		payload_bytes integer NOT NULL,
		parameters jsonb NOT NULL DEFAULT '{}'::jsonb,
		source_token_breakdown jsonb NOT NULL DEFAULT '{}'::jsonb,
		message_count integer NOT NULL,
		tool_count integer NOT NULL,
		capture_mode text NOT NULL,
		capture_content text NOT NULL DEFAULT '',
		capture_content_hash text NOT NULL DEFAULT '',
		capture_original_bytes integer NOT NULL,
		capture_stored_bytes integer NOT NULL,
		capture_redacted boolean NOT NULL DEFAULT false,
		capture_redaction_strategy text NOT NULL DEFAULT '',
		capture_redaction_count integer NOT NULL DEFAULT 0,
		capture_truncated boolean NOT NULL DEFAULT false,
		capture_reconstructable boolean NOT NULL DEFAULT false,
		capture_expires_at timestamptz,
		capture_expired boolean NOT NULL DEFAULT false,
		created_at timestamptz NOT NULL,
		UNIQUE(run_id, model_call_id, attempt)
	)`,
	`ALTER TABLE model_request_records ADD COLUMN IF NOT EXISTS source_token_breakdown jsonb NOT NULL DEFAULT '{}'::jsonb`,
	`ALTER TABLE model_request_records ADD COLUMN IF NOT EXISTS capture_redaction_strategy text NOT NULL DEFAULT ''`,
	`ALTER TABLE model_request_records ADD COLUMN IF NOT EXISTS capture_redaction_count integer NOT NULL DEFAULT 0`,
	`ALTER TABLE model_request_records ADD COLUMN IF NOT EXISTS capture_expires_at timestamptz`,
	`ALTER TABLE model_request_records ADD COLUMN IF NOT EXISTS capture_expired boolean NOT NULL DEFAULT false`,
	`CREATE INDEX IF NOT EXISTS model_request_records_run_created_idx ON model_request_records(run_id, created_at, id)`,
	`CREATE TABLE IF NOT EXISTS memories (
		id text PRIMARY KEY,
		workspace_id text NOT NULL DEFAULT 'default_workspace',
		user_id text,
		project_id text,
		conversation_id text REFERENCES conversations(id) ON DELETE SET NULL,
		run_id text REFERENCES runs(id) ON DELETE SET NULL,
		source_message_id text REFERENCES messages(id) ON DELETE SET NULL,
		kind text NOT NULL,
		content text NOT NULL,
		metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
		created_at timestamptz NOT NULL,
		updated_at timestamptz NOT NULL
	)`,
	`ALTER TABLE memories ADD COLUMN IF NOT EXISTS workspace_id text`,
	`UPDATE memories m SET workspace_id = c.workspace_id FROM conversations c WHERE c.id = m.conversation_id AND (m.workspace_id IS NULL OR BTRIM(m.workspace_id) = '' OR m.workspace_id = 'default')`,
	`UPDATE memories SET workspace_id = 'default_workspace' WHERE workspace_id IS NULL OR BTRIM(workspace_id) = '' OR workspace_id = 'default'`,
	`ALTER TABLE memories ALTER COLUMN workspace_id SET DEFAULT 'default_workspace'`,
	`ALTER TABLE memories ALTER COLUMN workspace_id SET NOT NULL`,
	`CREATE TABLE IF NOT EXISTS memory_candidates (
		id text PRIMARY KEY,
		conversation_id text REFERENCES conversations(id) ON DELETE CASCADE,
		run_id text REFERENCES runs(id) ON DELETE SET NULL,
		source_message_id text NOT NULL,
		source_role text NOT NULL,
		kind text NOT NULL,
		content text NOT NULL,
		status text NOT NULL,
		extraction_reason text NOT NULL,
		policy_reason text NOT NULL,
		confidence double precision NOT NULL DEFAULT 1,
		created_at timestamptz NOT NULL
	)`,
	`ALTER TABLE memory_candidates ADD COLUMN IF NOT EXISTS confidence double precision NOT NULL DEFAULT 1`,
	`CREATE TABLE IF NOT EXISTS memory_embeddings (
		memory_id text PRIMARY KEY REFERENCES memories(id) ON DELETE CASCADE,
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
				AND table_name = 'memory_embeddings'
				AND column_name = 'embedding'
				AND udt_name <> 'vector'
		) THEN
			ALTER TABLE memory_embeddings
				ALTER COLUMN embedding TYPE vector(1536)
				USING embedding::vector(1536);
		END IF;
	END $$`,
	`CREATE TABLE IF NOT EXISTS documents (
		id text PRIMARY KEY,
		workspace_id text NOT NULL DEFAULT 'default_workspace',
		title text NOT NULL,
		version text NOT NULL DEFAULT '',
		content_hash text NOT NULL DEFAULT '',
		source_type text NOT NULL,
		source_uri text,
		mime_type text,
		content text NOT NULL,
		metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
		created_at timestamptz NOT NULL,
		updated_at timestamptz NOT NULL
	)`,
	`ALTER TABLE documents ADD COLUMN IF NOT EXISTS workspace_id text`,
	`UPDATE documents SET workspace_id = 'default_workspace' WHERE workspace_id IS NULL OR BTRIM(workspace_id) = '' OR workspace_id = 'default'`,
	`ALTER TABLE documents ALTER COLUMN workspace_id SET DEFAULT 'default_workspace'`,
	`ALTER TABLE documents ALTER COLUMN workspace_id SET NOT NULL`,
	`ALTER TABLE documents ADD COLUMN IF NOT EXISTS version text NOT NULL DEFAULT ''`,
	`ALTER TABLE documents ADD COLUMN IF NOT EXISTS content_hash text NOT NULL DEFAULT ''`,
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
	`CREATE INDEX IF NOT EXISTS idx_runs_conversation_created ON runs(conversation_id, created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_runs_status_created ON runs(status, created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_run_delegations_parent_created ON run_delegations(parent_run_id, created_at ASC)`,
	`CREATE INDEX IF NOT EXISTS idx_messages_conversation_created ON messages(conversation_id, created_at ASC)`,
	`CREATE INDEX IF NOT EXISTS idx_document_chunks_parent_index ON document_chunks(document_id, parent_id, chunk_index)`,
	`CREATE INDEX IF NOT EXISTS idx_steps_run_created ON collaboration_steps(run_id, created_at ASC)`,
	`CREATE INDEX IF NOT EXISTS idx_run_events_run_sequence ON run_events(run_id, sequence ASC)`,
	`CREATE INDEX IF NOT EXISTS idx_run_events_conversation_timestamp ON run_events(conversation_id, timestamp ASC)`,
	`CREATE TABLE IF NOT EXISTS run_usage_entries (
		id text PRIMARY KEY,
		run_id text NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
		operation_id text NOT NULL,
		stage_id text,
		turn_id text,
		kind text NOT NULL,
		purpose text NOT NULL,
		model text NOT NULL DEFAULT '',
		tool_name text NOT NULL DEFAULT '',
		model_calls integer NOT NULL DEFAULT 0,
		tool_calls integer NOT NULL DEFAULT 0,
		prompt_tokens integer NOT NULL DEFAULT 0,
		completion_tokens integer NOT NULL DEFAULT 0,
		total_tokens integer NOT NULL DEFAULT 0,
		estimated_cost_micros bigint NOT NULL DEFAULT 0,
		estimated boolean NOT NULL DEFAULT false,
		timestamp timestamptz NOT NULL,
		UNIQUE(run_id, operation_id, kind)
	)`,
	`ALTER TABLE run_usage_entries ADD COLUMN IF NOT EXISTS operation_id text`,
	`UPDATE run_usage_entries SET operation_id = id WHERE operation_id IS NULL OR BTRIM(operation_id) = ''`,
	`ALTER TABLE run_usage_entries ALTER COLUMN operation_id SET NOT NULL`,
	`ALTER TABLE run_usage_entries ADD COLUMN IF NOT EXISTS purpose text`,
	`UPDATE run_usage_entries SET purpose = 'primary' WHERE purpose IS NULL OR BTRIM(purpose) = ''`,
	`ALTER TABLE run_usage_entries ALTER COLUMN purpose SET NOT NULL`,
	`ALTER TABLE run_usage_entries ADD COLUMN IF NOT EXISTS model text`,
	`UPDATE run_usage_entries SET model = '' WHERE model IS NULL`,
	`ALTER TABLE run_usage_entries ALTER COLUMN model SET DEFAULT ''`,
	`ALTER TABLE run_usage_entries ALTER COLUMN model SET NOT NULL`,
	`ALTER TABLE run_usage_entries ADD COLUMN IF NOT EXISTS tool_name text`,
	`UPDATE run_usage_entries SET tool_name = '' WHERE tool_name IS NULL`,
	`ALTER TABLE run_usage_entries ALTER COLUMN tool_name SET DEFAULT ''`,
	`ALTER TABLE run_usage_entries ALTER COLUMN tool_name SET NOT NULL`,
	`CREATE UNIQUE INDEX IF NOT EXISTS run_usage_entries_run_operation_kind_idx ON run_usage_entries(run_id, operation_id, kind)`,
	`CREATE INDEX IF NOT EXISTS run_usage_entries_run_timestamp_idx ON run_usage_entries(run_id, timestamp, id)`,
	`CREATE INDEX IF NOT EXISTS idx_context_compactions_conversation_created ON context_compactions(conversation_id, created_at ASC)`,
	`CREATE TABLE IF NOT EXISTS task_state_revisions (
		id text PRIMARY KEY,
		workspace_id text NOT NULL,
		conversation_id text NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
		version bigint NOT NULL,
		previous_version bigint NOT NULL,
		patch jsonb NOT NULL,
		state jsonb NOT NULL,
		source jsonb NOT NULL,
		created_at timestamptz NOT NULL,
		UNIQUE(conversation_id, version)
	)`,
	`CREATE INDEX IF NOT EXISTS task_state_revisions_conversation_version_idx ON task_state_revisions(conversation_id, version ASC)`,
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
