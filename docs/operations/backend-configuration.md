# Backend Configuration

Configuration is loaded at startup and injected by `app`. Use
[`apps/api/.env.example`](../../apps/api/.env.example) for API environment
inputs and defaults; this page explains deployment choices. For control scope,
units, precedence, and tuning, use [Execution controls](../runtime/execution-controls.md).
For startup commands, use [Local setup](../guides/local-setup.md).

## Configuration Files

| Template / ignored local file | Consumer |
| --- | --- |
| `apps/api/.env.example` / `apps/api/.env` | Go API runtime and credentials named by the supplied route examples |
| [`scripts/.env.example`](../../scripts/.env.example) / `scripts/.env` | Development launcher, Headless CLI, evaluation and opt-in tests |

Development, client, test and evaluation shell entry points load `scripts/.env`
using Node.js 22+ dotenv parsing,
never Shell evaluation. Existing process values, including empty values, win;
supported CLI flags override environment settings. A missing file retains each
program's defaults. `make dev` imports only its three launcher settings; the
Headless wrapper imports only its four client settings. Test/evaluation entry
points do not import development ports or the frontend API URL from the file.
Explicit process overrides still reach child processes.

The API loads only its own `.env`. The script loader never imports that file;
direct Go/npm commands and installed CLI binaries require script settings in the process
environment. Do not export the complete API file to the frontend or tests.
`LLAMA_CPP_API_KEY` and `RERANKER_API_KEY` have runtime and evaluation consumers;
configure them separately in each file as needed, without automatic secret
sharing. Live tests remain opt-in and require a dedicated test database.

OS variables and generated test identities are not project settings. Model/Tool
JSON and CLI flags retain their own contracts; environment entries do not replace
them. Custom credential names declared by operator JSON/CLI must also be supplied.
Empty credentials, authorized IDs and deployment facts have no usable default.
The backend template coverage test checks both templates against Go readers and
the supplied route catalogs; it never reads local credentials.

## Baseline Environment

| Setting | Deployment requirement |
| --- | --- |
| `DATABASE_URL` | Required PostgreSQL + pgvector; no File Store fallback |
| `MODEL_ROUTE_CONFIG_PATH` | Required non-empty peer Chat route catalog; default path resolves from API cwd |
| Credential variables named by routes | Required separately in process environment or ignored `.env`; never JSON/Snapshot values |
| `BIND_ADDRESS` / `PORT` | Loopback / 8080 by default; widen binding only behind an access boundary |
| `ALLOWED_ORIGINS` | Browser CORS origins, not authentication |
| `AUTH_MODE` / `OIDC_*` / `AUTH_*` | Trusted-local default or OIDC identity with IdP-controlled registration, automatic personal Workspace onboarding, revocable sessions and database-only Membership, see [identity setup](identity-membership.md) |
| `EMBEDDING_*` | Independent embedding endpoint/model/dimensions/timeout; dimensions must match stored vectors |
| `TOOL_CONFIG_PATH` | Operator enablement/security policy file, not application persistence |
| `TRUSTED_SKILL_DIRS` | Reviewed installation roots; relative to API process cwd |
| `FILESYSTEM_ROOTS_PATH` / `PROJECT_GUIDANCE_PATH` | Separate file-access and instruction-trust configuration; [project instructions](../context/project-instructions.md) |

The launcher uses `apps/api` as cwd. The template selects
`config/model-routes.json`, `.data/tools.json`, and `../../.agents/skills`.
Unset/empty Skill configuration disables bindings; `make setup` creates the
empty shared root. Do not commit local `.env`, credentials, or operator packages.

## Workspace Scope

Missing scope selects the owner's active default Workspace, never another
owner's data. [Workspace lifecycle](workspace-lifecycle.md) owns defaults,
archive/deletion and upgrade requirements;
[API access](../reference/api-reference.md#access-and-workspace-scope) owns selectors.

Route/embedding credentials are resolved at composition and passed directly to
clients, not stored in general Config or frozen records. Endpoints/model identity
are secret-free protocol. See [credential boundary](credential-boundary.md).

## Model and Embedding Providers

The secret-free route JSON defines stable ID, provider/model/endpoint,
capabilities, context/output limits, priority, generation profiles, pricing,
credential environment-variable name, and live request timeout. Copy/review
[the example](../../apps/api/config/model-routes.example.json); missing route
credentials fail startup instead of selecting simulation.

Routes use capability filtering, priority/ID ordering and Run affinity, not
automatic failover. [Model routing](../runtime/model-routing.md) owns selection,
generation profiles, frozen pricing and recovery compatibility.

Embedding never participates in Chat routing. The default Ollama endpoint is
`http://localhost:11434/api/embed`; set the actual model and dimension.
For credentialed OpenAI-compatible embeddings, use its `/v1` base URL and
`EMBEDDING_API_KEY`. A keyless non-Ollama call fails rather than returning
synthetic vectors. The offline `hash` evaluation profile uses a separate
embedding-only implementation, retaining `simulated / local_hash_embedding`
identity; it is not semantic quality evidence or a Chat fallback.

Stored columns use `vector(1536)`. Choose matching output dimensions or migrate
the vector columns; merely setting `EMBEDDING_DIMENSIONS` is not proof of
model support. A provider with configurable output dimensions may use a larger
model at 1536 dimensions. Reindex after provider/model/dimension/chunker changes:
unknown or mixed active index identities return `knowledge_index_incompatible`,
not partial search results. See [index lifecycle](../knowledge/knowledge-rag.md#index-lifecycle-and-compatibility).

## Concurrency, Rate Limits, and Retry

Tune Run admission, owner/global physical-request capacity and per-key RPM/TPM
as separate controls. Their variables, units, acquisition order, zero semantics
and errors are in [Execution controls](../runtime/execution-controls.md).
Chat timeout uses live route `request_timeout_seconds`; Embedding uses
`EMBEDDING_REQUEST_TIMEOUT`. Both include admission waits through body completion.

## Run Budget

`RUN_MAX_*` applies to new Runs; zero disables the named dimension. Configure
frozen token prices before relying on a monetary cap. [Run Budget](../runtime/run-budget.md)
owns accounting, cost units and Autonomous cap precedence.

`AUTONOMOUS_MAX_RUNTIME` accepts an optional duration and takes precedence over
`AUTONOMOUS_MAX_RUNTIME_SECONDS`. Its template value is empty so copying defaults
does not override an explicitly configured seconds limit.

## Model Request Capture

`MODEL_REQUEST_CAPTURE_MODE` defaults to `metadata_only`. Enable `redacted` or
`full` only with deliberate byte/retention limits. The
[Capture contract](../context/model-request-reconstruction.md#capture-modes) owns
stored content, credential downgrade, truncation and lazy expiry. Capture is
live observability policy, not frozen execution configuration.

## OpenTelemetry Traces

`OTEL_TRACES_EXPORTER=none` disables external tracing by default. The opt-in
`otlp` path exports metadata-only traces asynchronously; Request Capture settings
never enable telemetry body capture. See [OpenTelemetry traces](opentelemetry.md)
for supported settings, local Collector/Jaeger, bounds and verification.

## Runtime Invariants

Invariants report stable codes in `projection.invariant_failures` and log
without changing completion, mutating records, or making Replay unreadable.
There is no fail-mode setting. See [projections](../runtime/event-projections-runtime-invariants.md).

## Memory Provider

`MEMORY_SYNC_*` bounds background jobs; `MEMORY_PROVIDER_*` bounds storage retries,
not another layer of model retries. Adaptive extraction defaults to `shadow`;
review proposals before switching to `auto`. [Memory](../context/memory-management.md)
owns curation, model/storage retry separation, shutdown and failure semantics.

## Postgres + pgvector

Postgres startup applies idempotent schema migrations. HNSW supports vectors;
generated `tsvector`/GIN supports lexical search without another setting.
[Storage boundary](../architecture/storage-boundary.md) owns adapter/test
separation; startup migrations are not backup or rollback evidence.

## Tool Configuration

`MCP_CONFIG_PATH` enables operator-file connections; empty does not disable
Workspace MCP connections. Owner-managed Bearer storage requires
`MCP_CREDENTIAL_ENCRYPTION_KEY` (base64, 32 random bytes). The
[MCP guide](../tools/mcp-adapter.md) owns URL exceptions, asynchronous/required
discovery, connection configuration and read-only limits.

Missing Tool config defaults to enabled `calculator`, `get_current_time`,
`web_search`, and `web_fetch`. [Web fetch](../tools/web-fetch.md) reads only current-Run
search sources and needs no separate credential. Web search needs `TAVILY_API_KEY`, Agent allowlist membership,
and an exact egress/credential policy. New defaults include the rule; existing
custom policies and saved Agent choices are never silently widened.

Enablement is not readiness: missing credentials withhold model/Snapshot
definitions. [Tool security](../tools/tool-security-policy.md) owns authority;
[Agent selection](../runtime/agent-selection.md) owns required-dependency eligibility.

`TOOL_SCHEMA_MODE` and `TOOL_SCHEMA_TOKEN_THRESHOLD` configure frozen model
visibility, not authorization or a token budget. Eager remains the default;
see [Tool discovery](../tools/tool-discovery.md) for activation and Resume.

`TOOL_RESULT_MAX_BATCH_BYTES` and `TOOL_ARTIFACT_*` bound aggregate results,
immutable spills, previews, and persisted expiry; [Artifacts](../tools/tool-result-artifacts.md)
own details. `TOOL_PROGRESS_*` thresholds are frozen per Run; guard rejection
precedes Budget/Handler execution. See [Progress Guard](../tools/tool-progress-guard.md).

## Sandbox Commands

`SANDBOX_ENABLED` opts into scratch execution; it does not enable or authorize
the Tool. [Sandbox execution](sandbox-execution.md) owns compatible sbx setup,
resource caps, startup recovery, the authorization rule and cleanup semantics.

## Trusted Skill Packages

`TRUSTED_SKILL_DIRS` names reviewed installation roots, not individual packages.
See [installation](../tools/skill-installation.md#shared-directory-and-runtime-trust)
for cwd/layout checks and the [Loader contract](../tools/trusted-skills.md) for
discovery, package limits, Agent binding and frozen content.

## Verification

`completion_contract` opts a new Run in; `VERIFICATION_*` settings bound execution,
not activation. Host Command Verifier is retired. [Verification](../runtime/verification.md)
owns contract/Evidence behavior; [execution boundaries](execution-boundaries.md)
owns HTTP origins, DNS checks, timeouts and response caps.
