# Repository Instructions for Coding Agents

Keep this file short and operational. Stable architecture and product knowledge
lives in [docs/README.md](docs/README.md); follow links instead of expanding this
file into a second handbook.

## Working Principles

1. Think before editing. State assumptions and inspect the owning code first.
2. Prefer the smallest change that satisfies a verifiable outcome.
3. Preserve existing boundaries and local conventions unless the task requires
   an architectural change.
4. Do not revert or overwrite unrelated user changes in a dirty worktree.
5. Keep command output bounded. Use `rg` before slower search tools.

For commands with unknown output size:

```bash
COMMAND 2>&1 | head -c 30000
```

## Repository Map

```text
apps/api/                  Go backend
apps/web/                  Next.js workbench
docs/                      architecture and operations
frontend_uex_design.md     product-level frontend constraints
```

Start with:

- [Documentation guide](docs/README.md)
- [Backend architecture](docs/backend-architecture.md)
- [Internal terms](docs/terms.md)
- [Execution controls](docs/execution-controls.md)
- [Engineering decisions](docs/engineering-decisions.md)

## Backend Workflow

Run from `apps/api`. Use Go `1.26.5` through `gvm`.

```bash
source ~/.gvm/scripts/gvm
gvm use go1.26.5 >/dev/null
mkdir -p /private/tmp/agentflow-go-build-cache
GOCACHE=/private/tmp/agentflow-go-build-cache go test ./...
```

Run the server with:

```bash
GOCACHE=/private/tmp/agentflow-go-build-cache go run ./cmd/server
```

Backend rules:

- Keep `cmd/server` thin and construct production dependencies in `app`.
- Depend on the smallest capability interface required by a consumer.
- Keep HTTP parsing/SSE in `httpapi`; orchestration belongs in `agent`/`turn`.
- Keep retrieval policy in `rag`/`knowledge`, not in handlers or Store adapters.
- Add focused tests for changed behavior and failure boundaries.
- For API shape changes, test serialized JSON rather than structs alone.

## Database Schema Changes

Any persisted-field change must update and verify the complete path:

1. Domain model and JSON contract.
2. `CREATE TABLE IF NOT EXISTS` definition for new databases.
3. Idempotent `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` migration for existing
   databases.
4. Every affected `SELECT`, `Scan`, `INSERT`, and `UPDATE` column list.
5. File Store and Postgres behavior parity.
6. A migration regression test that asserts the required schema step exists.
7. A Postgres round-trip test when `TEST_DATABASE_URL` is available.

Before finishing, search for every reference to the table and column. A query
must never depend on a column that the startup migration does not create.

## Frontend Workflow

Run from `apps/web` with a supported Node.js version through `nvm`.

```bash
npm install
npm run lint
npm test
npm run build
```

Frontend rules:

- Follow [Frontend experience principles](frontend_uex_design.md).
- Add CSS to the narrowest owning module described in
  [Stylesheet organization](apps/web/app/styles/README.md).
- Preserve one vertical scroll owner; avoid nested cards and nested scrolling.
- Use existing component and Lucide icon patterns.
- Next.js may rewrite `next-env.d.ts` during build. Do not leave generated import
  churn in the final diff.

## RAG and Knowledge Changes

Read [Knowledge / RAG](docs/knowledge-rag.md) before changing ingestion,
retrieval, context selection, or transformation.

Verify the relevant stages independently:

- ingest and document detail;
- semantic and keyword recall;
- RRF ranks and versioned fusion metadata;
- reranking and relevance-gate filtering reasons;
- parent/adjacent expansion with scope preservation;
- context deduplication, merging, and token limits;
- prompt-injection decisions and untrusted-context boundaries;
- HTTP, Single-Agent, Multi-Agent, and Autonomous path consistency.

Do not claim calibrated retrieval quality without a representative Golden
Dataset. Do not claim multi-tenant isolation until Workspace lifecycle, scope,
and ACL enforcement are complete end to end.

## Documentation Changes

- `README.md` and `docs/README.md` are the public entry points.
- Update the owning document when behavior, API shape, configuration,
  persistence, events, or failure semantics change.
- Prefer concrete contracts, trade-offs, and evidence over claims such as
  "production-ready" or "intelligent".
- Keep terminology aligned with `docs/terms.md` and control ownership aligned
  with `docs/execution-controls.md`.
- State limitations explicitly; never imply a stronger security, evaluation, or
  tenant-isolation guarantee than the code provides.
- `docs/TODO/` is local planning material. Do not stage or commit it unless the
  user explicitly asks for it.

## Git and Scope

- Do not implement features directly on `main`. Update from `main` and create a
  scoped branch unless instructed otherwise.
- Stage explicit files only; never use `git add -A` in a dirty worktree.
- Do not stage local files such as `JD.md`, `RAG.md`, or `docs/TODO/` unless the
  user explicitly requests them.
- Do not create a commit, push, or PR unless requested.
- If blocked, ask for the missing input or propose a focused next step instead
  of broad speculative changes.
