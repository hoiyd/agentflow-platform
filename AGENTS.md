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
6. Before implementing a backlog item, search for complete and partial existing
   behavior. Extend or consolidate it instead of creating a parallel path.
7. Trace cross-layer changes through backend behavior, API contracts, generated
   clients, and frontend consumers before treating the work as complete.

For commands with unknown output size:

```bash
COMMAND 2>&1 | head -c 30000
```

## Repository Map

```text
apps/api/                  Go backend
apps/web/                  Next.js workbench
docs/                      architecture and operations
docs/architecture/frontend-experience.md
                           product-level frontend constraints
```

Start with:

- [Documentation guide](docs/README.md)
- [Backend architecture](docs/architecture/backend-architecture.md)
- [Internal terms](docs/architecture/terms.md)
- [Execution controls](docs/runtime/execution-controls.md)
- [Engineering decisions](docs/architecture/engineering-decisions.md)

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
- Use responsibility-specific filenames; avoid generic names such as
  `service.go` when the owner or behavior can be named directly.
- Keep HTTP parsing/SSE in `httpapi`; orchestration belongs in `agent`/`turn`.
- Keep retrieval policy in `rag`/`knowledge`, not in handlers or Store adapters.
- Add focused tests for changed behavior, failure paths, and state transitions.
- For API shape changes, test serialized JSON rather than structs alone.
- For API shape changes, regenerate the shared client and run
  `make contract-check` from the repository root.

## Database Schema Changes

Any persisted-field change must update and verify the complete path:

1. Domain model and JSON contract.
2. `CREATE TABLE IF NOT EXISTS` definition for new databases.
3. Idempotent `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` migration for existing
   databases.
4. Every affected `SELECT`, `Scan`, `INSERT`, and `UPDATE` column list.
5. PostgreSQL persistence behavior; update test/evaluation fixtures only where consumed.
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

- Follow [Frontend experience principles](docs/architecture/frontend-experience.md).
- Treat desktop as the supported product surface unless mobile behavior is
  explicitly requested.
- Add CSS to the narrowest owning module described in
  [Stylesheet organization](apps/web/app/styles/README.md).
- Preserve one vertical scroll owner; avoid nested cards and nested scrolling.
- Use existing component and Lucide icon patterns.
- Next.js may rewrite `next-env.d.ts` during build. Do not leave generated import
  churn in the final diff.

## RAG and Knowledge Changes

Read [Knowledge / RAG](docs/knowledge/knowledge-rag.md) before changing ingestion,
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
Dataset. Do not claim full multi-tenant authorization until authenticated
Workspace lifecycle, Membership/ACL enforcement, and cross-tenant release tests
are complete. Mandatory namespace filtering alone is not authorization.

## Documentation Changes

- `README.md` and `docs/README.md` are the public entry points.
- Update the owning document when behavior, API shape, configuration,
  persistence, events, or failure semantics change.
- Prefer concrete contracts, trade-offs, and evidence over claims such as
  "production-ready" or "intelligent".
- Keep public documentation product-focused. Store personal notes and private
  preparation outside the repository or under the untracked `docs/private/`.
- Distinguish deterministic fixture evidence from live-model measurements, and
  trace every published number to a retained report field.
- Keep terminology aligned with `docs/architecture/terms.md` and control
  ownership aligned with `docs/runtime/execution-controls.md`.
- State limitations explicitly; never imply a stronger security, evaluation, or
  tenant-isolation guarantee than the code provides.

## Verification Before Finishing

- Run focused tests while editing, then the relevant package or application
  checks before reporting completion.
- Non-trivial behavior needs both a success case and its important failure path.
  When CI measures patch coverage, target at least 90% without filler tests.
- Treat sandbox, network, provider quota, and unavailable external services as
  environment failures; do not misreport them as code regressions.

## Git and Scope

- Do not implement features directly on `main`. Update from `main` and create a
  scoped branch unless instructed otherwise.
- When branching from `origin/main`, create the feature branch with `--no-track`.
  Never leave a feature branch tracking `origin/main`.
- Before pushing, inspect `@{upstream}`. If it differs from
  `origin/<current-branch>`, unset it before the first push; do not rely on
  `push.default` to choose the destination.
- On the first push, use the explicit refspec
  `git push -u origin HEAD:refs/heads/<current-branch>`, then verify
  `git rev-parse --abbrev-ref --symbolic-full-name @{upstream}` returns the same
  branch name on `origin` before reporting the push as complete.
- Stage explicit files only; never use `git add -A` in a dirty worktree.
- Before pushing, inspect staged filenames and content for credentials, `.env`
  files, local paths, private notes, and generated evidence containing prompts.
- Do not stage local files such as `docs/private/` unless explicitly requested.
- Do not create a commit, push, or PR unless requested.
- If blocked, ask for the missing input or propose a focused next step instead
  of broad speculative changes.
