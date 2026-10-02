# Local Setup

Run setup commands from the repository root unless a step explicitly changes
directory. AgentFlow is intended for a trusted development environment;
its default local mode is unauthenticated. Optional [OIDC identity and Workspace
membership](../operations/identity-membership.md) require separate provider
configuration and do not complete object-level authorization.

## Prerequisites

- Go 1.26.5 installed through gvm.
- Node.js 22+ with npm.
- Running PostgreSQL with pgvector and a configured database.
- A credentialed OpenAI-compatible Chat route; optionally Ollama for embeddings.

No script here starts PostgreSQL or installs a model server. Simulation belongs
to explicit offline fixtures, not the application fallback path.

## Configure and Start

```bash
test -f apps/api/.env || cp apps/api/.env.example apps/api/.env
test -f apps/api/config/model-routes.json || cp apps/api/config/model-routes.example.json apps/api/config/model-routes.json
```

Edit local configuration before starting:

1. Set `DATABASE_URL` to PostgreSQL + pgvector.
2. Review the complete peer route catalog in `apps/api/config/model-routes.json`:
   endpoint, model, supported capabilities, limits, generation profiles, and
   credential variable names. Set those credentials in the process environment
   or ignored `apps/api/.env`, never in the route JSON.
3. Set the embedding endpoint/model/dimension. Stored vectors use
   `vector(1536)`; the actual embedding output must match. Changing provider,
   model, dimension, or chunker requires reindexing existing documents.
4. Leave the API at `BIND_ADDRESS=127.0.0.1` unless it is behind an intentional
   external access boundary. CORS origins do not provide authentication.

See [backend configuration](../operations/backend-configuration.md) for settings
and [model routing](../runtime/model-routing.md) for selection semantics.

```bash
make quickstart               # Locked frontend install, Go modules, API + web
make dev                      # Subsequent starts without dependency installation
API_PORT=18080 WEB_PORT=13000 make dev   # Alternative ports
```

The default API is `http://127.0.0.1:8080`; the workbench is
[localhost:3000/workspace](http://localhost:3000/workspace).
Press Ctrl+C once to stop both processes. Startup fails for missing database,
route catalog, or required credentials. There is no file-backed Store fallback.

The launcher runs the API with `apps/api` as its working directory. Relative
config paths, `.env`, and `TRUSTED_SKILL_DIRS=../../.agents/skills` use that cwd.
Install reviewed Skills from the repository root using the
[Skill installation guide](../tools/skill-installation.md), not from the API cwd.

## Manual Start

After configuration, start the backend in one terminal:

```bash
mkdir -p .agents/skills
cd apps/api
source ~/.gvm/scripts/gvm
gvm use go1.26.5
GOCACHE=/private/tmp/agentflow-go-build-cache go run ./cmd/server
```

Start the frontend in another:

```bash
cd apps/web
npm ci
npm run dev
```

Set `NEXT_PUBLIC_API_BASE_URL` only when the API is elsewhere.
Workspace selection uses the current owner's entities and persisted active
default; no frontend namespace environment variable is needed. Existing databases
require the [ownership migration](../operations/workspace-lifecycle.md#legacy-migration)
before running the upgraded API. New databases create the local owner's space
automatically.

## Tests and Offline Evidence

```bash
make test
make contract-check
make golden-eval
make context-eval
make routing-eval
make benchmark-evidence
```

`make test` runs Go tests, frontend lint, library/component tests, and the
production build. Without `TEST_DATABASE_URL`, database tests may skip;
that is not persistence acceptance. Use only a dedicated test database, never
application data. Some suites create disposable databases; older integration
suites use the dedicated database directly. See
[storage test boundaries](../architecture/storage-boundary.md#verification).

For browser -> Next -> Go -> Postgres checks, follow
[functional regression gates](../operations/functional-regression-testing.md):
Chromium, pgvector, a test database with CREATEDB, and free ports 13000/18080
are required. Select affected cases rather than running the entire E2E suite
by default. Do not run Next builds concurrently in the same checkout.

The offline evaluation commands need no API key, running application, or model
network access once dependencies are installed. They prove deterministic
regression contracts, not live-model quality. The
[five-minute demo](demo.md) explains online and no-network evidence paths.
