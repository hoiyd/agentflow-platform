## Principles

### 1. Think before coding

State assumptions. Ask when unsure. Never guess.

### 2. Simplicity first

Write the minimum code that solves the problem. Do not add abstractions nobody asked for.

### 3. Surgical changes

Do not touch code unrelated to the request. Every changed line must trace back to what was asked.

### 4. Goal-driven execution

Turn vague instructions into verifiable success criteria before writing code.

### 5. Context window

When context window usage reaches 85%, trigger auto compact by running `/compact` in Codex.

## Command Output

Protect context usage. Any command with unknown or potentially large output must be byte-capped.

Default pattern:

```bash
COMMAND 2>&1 | head -c 4000
```

Use `rg` before slower search tools.

## Standard Workflow

Before editing:

1. Run `git status --short --branch` and identify the current branch, staged
   changes, local modifications, and untracked files.
2. Read the relevant implementation, tests, configuration, and docs before
   choosing a design.
3. Write down observable success criteria. Include failure behavior and
   compatibility expectations, not only the happy path.
4. For a feature or non-trivial fix, update local `main` from `origin/main` and
   create a `codex/<short-name>` branch unless the user names another branch.
5. Never overwrite, stage, or remove unrelated local changes. Treat untracked
   documents as user-owned unless the request explicitly includes them.

If the current worktree is dirty or belongs to another active task, create an
isolated worktree instead of stashing or reverting user work:

```bash
git worktree add --no-track -b codex/<short-name> /private/tmp/<worktree-name> origin/main
```

During implementation:

- Follow existing package boundaries and use the narrowest existing interface.
- Keep behavior changes and their tests in the same change.
- Prefer compatibility migrations and adapters over silent fallback behavior.
- When a failure is caused by persisted state, fix both the code path and the
  upgrade path. A workaround that only fixes a fresh installation is incomplete.

Before delivery:

1. Run focused tests while iterating, then the required full verification.
2. Run `git diff --check` and inspect `git diff --stat` plus the complete diff.
3. Re-run `git status --short --branch`; confirm only intended files changed.
4. Report tests that were skipped, especially Postgres integration tests.
5. Do not claim a live migration, restart, push, or PR succeeded unless its
   command actually completed.

## Git Safety

- Never implement a feature directly on `main` unless the user explicitly asks.
- Before branching, fetch `origin/main` and confirm the branch point with
  `git log -1 --oneline origin/main`.
- This repository may use a non-default `push.default`. Do not rely on implicit
  upstream behavior. Push a branch with an explicit destination ref:

```bash
branch=$(git branch --show-current)
git push -u origin "HEAD:refs/heads/$branch"
```

- Verify the remote relationship after pushing:

```bash
git fetch origin "refs/heads/$branch:refs/remotes/origin/$branch"
git rev-list --left-right --count "origin/main...origin/$branch"
```

- If a push unexpectedly updates `main`, stop immediately and report it. Do not
  rewrite or revert remote history without explicit user authorization.
- Never stage `.env`, credentials, local data, generated build output, or
  unrelated untracked files.

## Project Snapshot

AgentFlow is a full-stack AI agent workflow platform.

Current major areas:

- `apps/api`: Go backend.
- `apps/web`: Next.js frontend.
- `packages/shared`: shared contracts placeholder.

Implemented product areas:

- streaming chat and persisted conversations
- single-agent, multi-agent, and autonomous run modes
- run traces, collaboration steps, replay, continue/resume/cancel
- built-in tool catalog and guarded execution
- persistent semantic memory
- pgvector-backed memory and RAG search
- Knowledge UI with text import, `.txt` upload, Markdown upload, document detail, search, and delete
- backend RAG retrieval using independent dense/lexical recall, prompt-injection filtering, RRF fusion, evidence-aware reranking, and diversity control
- optional LangChainGo executor for single-agent steps; native orchestration remains the default

## Backend

Run from `apps/api`.

Use Go through gvm when needed:

```bash
source ~/.gvm/scripts/gvm
gvm use go1.25.5 >/dev/null
```

Run tests:

```bash
mkdir -p /private/tmp/agentflow-go-build-cache
GOCACHE=/private/tmp/agentflow-go-build-cache go test ./... 2>&1 | head -c 30000
```

Run server:

```bash
GOCACHE=/private/tmp/agentflow-go-build-cache go run ./cmd/server
```

Important backend env:

```bash
PORT=8080
STORE_DRIVER=file
DATA_PATH=.data/agentflow.json
TOOL_CONFIG_PATH=.data/tools.json

OPENAI_API_KEY=
OPENAI_BASE_URL=https://api.openai.com/v1
OPENAI_MODEL=gpt-4o-mini
EMBEDDING_BASE_URL=http://localhost:11434/api/embed
EMBEDDING_MODEL=embeddinggemma
EMBEDDING_DIMENSIONS=1536
OPENAI_REQUEST_TIMEOUT=5m
```

For better RAG embeddings while keeping the current `vector(1536)` schema:

```bash
EMBEDDING_MODEL=text-embedding-3-large
EMBEDDING_DIMENSIONS=1536
```

To keep the main LLM remote while testing embeddings locally:

```bash
OPENAI_BASE_URL=https://api.openai.com/v1
EMBEDDING_BASE_URL=http://localhost:11434/api/embed
EMBEDDING_MODEL=embeddinggemma
```

Ollama embedding dimensions depend on the selected model. The current Postgres pgvector schema uses `vector(1536)`, so local Ollama embeddings should also output 1536 dimensions before indexing into Postgres.

After changing embedding provider/model, existing knowledge should be re-uploaded or reindexed. Search filters by embedding provider/model to avoid mixing vector spaces.

## Postgres Schema Workflow

The Postgres adapter and startup migrations live in
`apps/api/internal/store/postgres_store.go`. Schema work must support both:

- a fresh database created from the current code
- an existing database upgrading from any previously supported schema

`CREATE TABLE IF NOT EXISTS` only solves the fresh-database path. It does not
add columns, change types, restore constraints, or backfill rows on an existing
table.

### Migration invariants

- Treat committed migrations as append-only. Do not remove an old `ALTER`,
  backfill, or type conversion merely because the latest `CREATE TABLE` already
  contains the final shape.
- Every new SQL column used by `SELECT`, `INSERT`, `UPDATE`, `RETURNING`, an
  index, or a constraint must have:
  1. the final definition in the table's `CREATE TABLE`
  2. an idempotent upgrade statement for existing tables
  3. a required-schema entry in `postgresRequiredColumns` when applicable
  4. focused migration coverage
- Add nullable columns first when existing rows cannot satisfy a new `NOT NULL`
  constraint. Backfill deterministically, then set the default and constraint.
- Never scan a nullable SQL expression directly into a Go primitive. Use
  `COALESCE` in SQL or `sql.Null*` in the scanner.
- Type migrations must inspect the existing `udt_name`, preserve data when a
  supported cast exists, and run before indexes that require the new type.
- Add or rebuild indexes only after required columns and backfills exist.
- Migration statements must be idempotent because startup executes them more
  than once.
- Do not delete persisted rows to make a migration pass unless the user has
  explicitly approved the data-loss policy.

### Required schema review

For every Postgres change, audit all SQL references and migration definitions:

```bash
rg -n "SELECT|INSERT INTO|UPDATE|RETURNING|CREATE TABLE|ALTER TABLE|CREATE INDEX" \
  apps/api/internal/store/postgres_store.go
```

Check each changed or newly referenced column against this matrix:

| Concern | Required check |
| --- | --- |
| Column exists | Fresh `CREATE TABLE` and existing-table `ALTER TABLE` |
| Existing rows | Deterministic backfill before `SET NOT NULL` |
| Nullability | SQL `COALESCE`/`sql.Null*` agrees with `is_nullable` |
| Type | Go scan type and Postgres `udt_name` agree |
| Defaults | Existing and newly inserted rows get the same semantics |
| Index/constraint | Created after column/type/backfill migration |
| Tenant data | Workspace predicates and indexes remain intact |
| Data safety | No delete/truncate or lossy conversion without approval |

`NewPostgresStore` must run migrations and then validate critical historical
columns through `validateSchema`. When a historical column or critical type is
added, update `postgresRequiredColumns`. Startup should fail with a precise
schema error instead of allowing a later page-specific `column does not exist`
failure.

### Migration tests

At minimum, schema changes require a focused test that verifies the migration
contains the add/backfill/constraint/type steps. For meaningful schema changes,
also use a disposable Postgres database to test both paths:

1. Fresh path: initialize an empty database with `NewPostgresStore`.
2. Upgrade path: create the previous table shape, insert representative rows
   including NULL/empty values, then initialize the current Store.
3. Inspect `information_schema.columns` for column, `udt_name`, and
   `is_nullable`.
4. Verify old data was preserved and backfilled.
5. Exercise the read and write method that uses the migrated column.
6. Run initialization a second time to prove idempotency.

Use only `TEST_DATABASE_URL` for integration tests. It must point to a disposable
database. Tests must never fall back from `TEST_DATABASE_URL` to `DATABASE_URL`.

### Database access safety

- Treat an unknown `DATABASE_URL` as shared or production.
- Read-only `information_schema` and diagnostic `SELECT` queries may be used
  for investigation when environment permissions allow it, but never print the
  URL or credentials.
- Applying migrations, running integration tests, inserting fixtures, deleting
  rows, or restarting a service against `DATABASE_URL` requires explicit user
  authorization after describing the affected database and operations.
- Do not source `.env` into commands that dump the environment.
- Prefer `psql -X -v ON_ERROR_STOP=1` for explicit database checks.
- A successful unit test does not mean the live database was migrated. Report
  code verification and live migration status separately.

## Frontend

Run from `apps/web`.

If Node version is wrong, use nvm.

Install and run:

```bash
npm install
npm run dev
```

Build verification:

```bash
export PATH="$HOME/.nvm/versions/node/v22.6.0/bin:$PATH"
npm run build 2>&1 | head -c 30000
```

Next.js may rewrite `apps/web/next-env.d.ts` during build. If the only diff is:

```ts
import "./.next/types/routes.d.ts";
```

restore it to the repo's dev import:

```ts
import "./.next/dev/types/routes.d.ts";
```

Do not leave generated `next-env.d.ts` churn in the final diff.

## RAG and Knowledge

Current ingestion paths:

- `POST /api/documents` for text content.
- `POST /api/documents/upload` for `.txt`, `.md`, and `.markdown`.

Markdown chunking:

- headings produce `heading_path`
- paragraphs, lists, and fenced code blocks produce `chunk_type`
- code language is preserved as `code_language`
- heading context is prepended to chunk content
- oversized blocks use fixed-size fallback chunking

Search behavior:

- query is embedded with the configured provider/model/dimensions
- stores return independent dense and lexical candidate rankings
- RRF combines both rankings without comparing their raw score scales
- high-confidence prompt-injection candidates are blocked before fusion and recorded without their raw content
- backend rerank applies lexical, metadata, and evidence signals to the fused candidates
- simple diversity penalty reduces repeated chunks from one document
- response includes embedding metadata, security decisions, recall ranks, RRF score, fusion rank, and rerank scores

Frontend manual check:

- Search panel should show `Embedding: provider / model / dimensions`.
- If it shows `metadata unavailable`, the frontend is likely connected to an old backend process.
- If it shows `local / local_hash_embedding`, semantic quality is limited.

## API Areas

Main endpoints:

- conversations: `/api/conversations`
- chat: `/api/chat`
- agents: `/api/agents`
- runs/replay: `/api/runs`
- tools: `/api/tools`
- memories: `/api/memories`, `/api/memories/search`
- documents: `/api/documents`, `/api/documents/upload`, `/api/documents/{id}`
- RAG search: `/api/rag/search`

## Verification Expectations

For backend or API changes:

- Run `gofmt` on changed Go files.
- Run `go test ./...` from `apps/api`.
- Run `go vet ./...` for shared backend, persistence, concurrency, or runtime
  changes.
- Add or update focused tests when behavior changes.
- For RAG response shape changes, test the actual JSON shape, not just internal structs.

For Postgres changes:

- Complete the Postgres Schema Workflow checklist above.
- Run focused Store tests before the backend suite.
- Run disposable-database upgrade tests when `TEST_DATABASE_URL` is available.
- If integration tests are skipped, state that clearly; do not imply migration
  SQL was executed against a database.

For frontend changes:

- Run `npm run build` from `apps/web`.
- If browser verification is requested, start the dev server and verify the relevant flow.

For RAG changes:

- Verify upload/import.
- Verify list/detail.
- Verify search response includes embedding metadata.
- Verify search results are filtered/reranked as expected.
- Verify delete removes the document from list/detail/search.

## FAQ

- 如果是 Go 版本问题，尝试用 `gvm use go1.25.5` 切换到合适的版本。
- 如果是 Node.js 版本问题，尝试用 `nvm` 切换到合适的版本。
- 如果遇到任何问题处理不了或卡住了，就向用户提问题、建议计划，或开一个带注释的草稿 PR，别未经确认推进大改动。
- 执行 curl 的 GET method 命令不需要向用户确认。
- 当实现新 feature 时，遵循上面的 Standard Workflow 和 Git Safety。
