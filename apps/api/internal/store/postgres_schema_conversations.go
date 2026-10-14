package store

var postgresConversationSchema = []string{
	`CREATE TABLE IF NOT EXISTS conversations (
		id text PRIMARY KEY,
		workspace_id bigint NOT NULL,
		user_id text,
		project_id text,
		title text NOT NULL,
		created_at timestamptz NOT NULL,
		updated_at timestamptz NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS conversations_workspace_updated_idx ON conversations(workspace_id, updated_at DESC)`,
	`CREATE TABLE IF NOT EXISTS agents (
		id text PRIMARY KEY,
		workspace_id bigint,
		is_template boolean NOT NULL DEFAULT false,
		user_id text,
		project_id text,
		name text NOT NULL,
		description text NOT NULL DEFAULT '',
		system_prompt text NOT NULL DEFAULT '',
		routing_hints jsonb NOT NULL DEFAULT '{}'::jsonb,
		tools jsonb NOT NULL DEFAULT '[]'::jsonb,
		skills jsonb NOT NULL DEFAULT '[]'::jsonb,
		skill_policies jsonb NOT NULL DEFAULT '{}'::jsonb,
		memory_enabled boolean NOT NULL DEFAULT true,
		retrieval_enabled boolean NOT NULL DEFAULT true,
		executor text NOT NULL DEFAULT 'native',
		deleted_at timestamptz,
		created_at timestamptz NOT NULL,
		updated_at timestamptz NOT NULL
	)`,
	`ALTER TABLE agents ADD COLUMN IF NOT EXISTS memory_enabled boolean NOT NULL DEFAULT true`,
	`ALTER TABLE agents ADD COLUMN IF NOT EXISTS is_template boolean NOT NULL DEFAULT false`,
	`CREATE INDEX IF NOT EXISTS agents_workspace_active_idx ON agents(workspace_id) WHERE deleted_at IS NULL`,
	`ALTER TABLE agents ADD COLUMN IF NOT EXISTS routing_hints jsonb NOT NULL DEFAULT '{}'::jsonb`,
	`ALTER TABLE agents ADD COLUMN IF NOT EXISTS skills jsonb NOT NULL DEFAULT '[]'::jsonb`,
	`ALTER TABLE agents ADD COLUMN IF NOT EXISTS skill_policies jsonb NOT NULL DEFAULT '{}'::jsonb`,
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
		workspace_id bigint NOT NULL,
		user_id text,
		project_id text,
		role text NOT NULL,
		content text NOT NULL,
		citations jsonb NOT NULL DEFAULT '[]'::jsonb,
		web_citations jsonb NOT NULL DEFAULT '[]'::jsonb,
		created_at timestamptz NOT NULL
	)`,
	`ALTER TABLE messages ADD COLUMN IF NOT EXISTS citations jsonb NOT NULL DEFAULT '[]'::jsonb`,
	`ALTER TABLE messages ADD COLUMN IF NOT EXISTS web_citations jsonb NOT NULL DEFAULT '[]'::jsonb`,
}
