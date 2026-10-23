package store

var postgresIdentitySchema = []string{
	`CREATE EXTENSION IF NOT EXISTS vector`,
	`CREATE TABLE IF NOT EXISTS auth_users (
		id text PRIMARY KEY,
		issuer text NOT NULL,
		subject text NOT NULL,
		name text NOT NULL DEFAULT '',
		UNIQUE (issuer,subject)
	)`,
	workspaceEntitySchema,
	`CREATE TABLE IF NOT EXISTS mcp_connections (
		id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
		workspace_id bigint NOT NULL REFERENCES workspaces(id),
		name text NOT NULL CHECK(char_length(btrim(name)) BETWEEN 1 AND 80),
		url text NOT NULL,
		auth_mode text NOT NULL CHECK(auth_mode IN ('none','bearer')),
		enabled boolean NOT NULL DEFAULT false,
		read_only_tools jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(read_only_tools)='array'),
		revision bigint NOT NULL DEFAULT 1,
		status text NOT NULL DEFAULT 'configured',
		credential_ciphertext text NOT NULL DEFAULT '',
		discovery jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(discovery)='array'),
		created_at timestamptz NOT NULL DEFAULT NOW(),
		updated_at timestamptz NOT NULL DEFAULT NOW(),
		deleted_at timestamptz
	)`,
	`CREATE INDEX IF NOT EXISTS mcp_connections_workspace_idx ON mcp_connections(workspace_id) WHERE deleted_at IS NULL`,
	`CREATE TABLE IF NOT EXISTS workspace_tool_config (
		workspace_id bigint PRIMARY KEY REFERENCES workspaces(id),
		allowed_tools jsonb NOT NULL DEFAULT '[]' CHECK(jsonb_typeof(allowed_tools)='array'),
		revision bigint NOT NULL DEFAULT 1,
		updated_at timestamptz NOT NULL DEFAULT NOW()
	)`,
	`CREATE TABLE IF NOT EXISTS auth_memberships (
		user_id text NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
		workspace_id bigint NOT NULL,
		PRIMARY KEY (user_id,workspace_id)
	)`,
	`CREATE TABLE IF NOT EXISTS auth_personal_workspaces (
		user_id text PRIMARY KEY REFERENCES auth_users(id) ON DELETE CASCADE,
		workspace_id bigint UNIQUE,
		created_at timestamptz NOT NULL DEFAULT NOW()
	)`,
	`CREATE TABLE IF NOT EXISTS auth_sessions (
		token_hash text PRIMARY KEY,
		user_id text NOT NULL REFERENCES auth_users(id) ON DELETE CASCADE,
		expires_at timestamptz NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS auth_sessions_expiry_idx ON auth_sessions(expires_at)`,
	`CREATE TABLE IF NOT EXISTS auth_login_attempts (
		state_hash text PRIMARY KEY,
		nonce text NOT NULL,
		verifier text NOT NULL,
		expires_at timestamptz NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS auth_login_attempts_expiry_idx ON auth_login_attempts(expires_at)`,
}
