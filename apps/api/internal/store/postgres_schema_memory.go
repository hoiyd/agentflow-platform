package store

var postgresMemorySchema = []string{
	`CREATE TABLE IF NOT EXISTS memories (
		id text PRIMARY KEY,
		version bigint NOT NULL DEFAULT 1,
		deleted_at timestamptz,
		workspace_id bigint NOT NULL,
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
	`ALTER TABLE memories ADD COLUMN IF NOT EXISTS version bigint NOT NULL DEFAULT 1`,
	`ALTER TABLE memories ADD COLUMN IF NOT EXISTS deleted_at timestamptz`,
	`CREATE TABLE IF NOT EXISTS memory_changes (
		workspace_id bigint NOT NULL,
		memory_id text NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
		operation_id text NOT NULL,
		command_hash text NOT NULL,
		action text NOT NULL,
		previous_version bigint NOT NULL,
		version bigint NOT NULL,
		source_message_id text NOT NULL DEFAULT '',
		actor text NOT NULL,
		reason text NOT NULL,
		created_at timestamptz NOT NULL,
		PRIMARY KEY (workspace_id, operation_id)
	)`,
	`CREATE INDEX IF NOT EXISTS memory_changes_history_idx ON memory_changes(memory_id, version DESC)`,
	`CREATE INDEX IF NOT EXISTS memory_changes_source_idx ON memory_changes(source_message_id)`,
	`CREATE TABLE IF NOT EXISTS memory_candidates (
		id text PRIMARY KEY,
		workspace_id bigint NOT NULL,
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
}
