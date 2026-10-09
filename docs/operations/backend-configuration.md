# Backend Configuration

Configuration is loaded at startup and injected by `app`. Use
[`.env.example`](../../apps/api/.env.example) for the maintained environment
list and defaults; this page explains deployment choices. For control scope,
units, precedence, and tuning, use [Execution controls](../runtime/execution-controls.md).
For startup commands, use [Local setup](../guides/local-setup.md).

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

The launcher uses `apps/api` as cwd. The template selects
`config/model-routes.json`, `.data/tools.json`, and `../../.agents/skills`.
Unset/empty Skill configuration disables bindings; `make setup` creates the
empty shared root. Do not commit local `.env`, credentials, or operator packages.

## Workspace Scope

All resource operations resolve an existing owned Workspace entity. Missing scope
selects the owner's active default; local mode uses a reserved persistent local
User with its own space, never another OIDC owner's data. Header/query/payload
selectors must agree; legacy string IDs are not runtime aliases.

The frontend loads `/api/workspaces` after the session probe and validates its
remembered selection before mounting consumers. `NEXT_PUBLIC_WORKSPACE_ID` is no
longer used. Archived spaces are read-only; foreign/deleted spaces return 404.
See [Workspace lifecycle and required old-data migration](workspace-lifecycle.md)
and [API access](../reference/api-reference.md#access-and-workspace-scope).

Route/embedding credentials are resolved at composition and passed directly to
clients, not stored in general Config or frozen records. Endpoints/model identity
are secret-free protocol. See [credential boundary](credential-boundary.md).

## Model and Embedding Providers

The secret-free route JSON defines stable ID, provider/model/endpoint,
capabilities, context/output limits, priority, generation profiles, pricing,
credential environment-variable name, and live request timeout. Copy/review
[the example](../../apps/api/config/model-routes.example.json); missing route
credentials fail startup instead of selecting simulation.

The first eligible route is selected by priority, then stable ID. Its affinity
is reused across the Run, compaction, adaptive extraction, and title generation.
All clients share model concurrency; per-key RPM/TPM stay separate. Failed calls
are not retried on another route. Frozen generation parameters are not assumed
to produce deterministic output. See [model routing](../runtime/model-routing.md).

Embedding never participates in Chat routing. The default Ollama endpoint is
`http://localhost:11434/api/embed`; set the actual model and dimension.
For credentialed OpenAI-compatible embeddings, use its `/v1` base URL and
`EMBEDDING_API_KEY`. A keyless non-Ollama call fails rather than returning
synthetic vectors. Offline `simulated / local_hash_embedding` is explicit and
not quality evidence.

Stored columns use `vector(1536)`. Choose matching output dimensions or migrate
the vector columns; merely setting `EMBEDDING_DIMENSIONS` is not proof of
model support. A provider with configurable output dimensions may use a larger
model at 1536 dimensions. Reindex after provider/model/dimension/chunker changes:
unknown or mixed active index identities return `knowledge_index_incompatible`,
not partial search results. See [index lifecycle](../knowledge/knowledge-rag.md#index-lifecycle-and-compatibility).

## Concurrency, Rate Limits, and Retry

| Variables | Owner / distinction |
| --- | --- |
| `MAX_CONCURRENT_RUNS`, `RUN_QUEUE_SIZE`, `RUN_QUEUE_WAIT_TIMEOUT` | Active tasks plus bounded waiting; same-Conversation single writer; full/expired waits return 429/503 + Retry-After |
| `MAX_CONCURRENT_MODEL_REQUESTS` | In-flight Chat/Embedding HTTP requests, not model count; streams hold a slot until body close |
| `MAX_CONCURRENT_OWNER_MODEL_REQUESTS` | One authenticated owner across all Workspaces; admitted attempts include downstream key/global wait; positive and below global (global >= 2) |
| `OWNER_MODEL_QUEUE_SIZE`, `OWNER_MODEL_QUEUE_WAIT_TIMEOUT` | Additional pending physical attempts per owner and owner-slot wait; 0 queue means reject immediately; route timeout can expire sooner |
| `MODEL_REQUESTS_PER_MINUTE` / `MODEL_TOKENS_PER_MINUTE` | Per-key request/input-estimate buckets; zero disables that bucket |
| `MODEL_RETRY_*` | Physical attempts/backoff inside one logical call; retries reacquire permits, backoff holds none |

Chat timeout uses the live route `request_timeout_seconds`; Embedding uses
`EMBEDDING_REQUEST_TIMEOUT`. Local wait phases and provider work are measured
separately; admission expiry is not transient provider failure.
[Execution controls](../runtime/execution-controls.md#3-provider-timeout-and-retry)
owns retryable classes, stream cutoff, and capacity errors.

## Run Budget

`RUN_MAX_*` freezes cumulative logical calls, prompt/completion/total tokens,
Tool calls, active runtime, and estimated cost for new Runs. Zero disables the
named dimension. Queue/human waiting is not active runtime; physical provider
retries share a logical reservation.

Prices and maximum cost are USD converted to integer microdollars. Cost is
unenforced at zero; configure input/output prices before enabling it. Autonomous
runtime/Tool caps fold once into the stricter Run Budget; iterations and output
characters remain mode-owned. See [Run Budget](../runtime/run-budget.md) for
settlement, purpose, and overage semantics.

## Model Request Capture

| `MODEL_REQUEST_CAPTURE_MODE` | Retained content |
| --- | --- |
| `metadata_only` (default) | Secret-free Envelope, canonical transport hash/counts, effective parameters, Snapshot/Manifest references; no prompt body |
| `redacted` | Bounded canonical JSON after deterministic redaction |
| `full` | Exact bounded JSON for trusted local debugging; detected credentials downgrade that Capture to redacted |

`MODEL_REQUEST_CAPTURE_MAX_BYTES` caps only retained body content; oversized
captures keep metadata and are marked truncated. Retention expiry lazily purges
content on Postgres reads, not Envelopes/redaction metadata. Capture policy is
live observability, not frozen model-input semantics. See
[request reconstruction](../context/model-request-reconstruction.md).

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

`MEMORY_SYNC_QUEUE_SIZE` / `MEMORY_SYNC_JOB_TIMEOUT` bound asynchronous jobs.
`MEMORY_PROVIDER_MAX_ATTEMPTS` includes the initial attempt; the retry delay
covers transient embedding/persistence failures. Accepted jobs drain at shutdown;
a full queue only rejects auxiliary work, never an already completed answer.
Recall failure degrades to no Memory with typed diagnostics.

Explicit durability rules are model-free. Adaptive extraction runs only when
rules find no Candidate: `off` disables it, `shadow` (default) audits proposals
without commit, `auto` commits only above `MEMORY_ADAPTIVE_MIN_CONFIDENCE`
and after deterministic safety checks. It uses the originating Run's selected
Chat model and shared request limits. See [Memory](../context/memory-management.md).

## Postgres + pgvector

Postgres performs idempotent startup migrations for execution records, usage,
request capture, Memory, Knowledge, and their supporting evidence. HNSW indexes
support vectors; generated `tsvector`/GIN indexes support lexical search without
another setting. Offline lexical fixtures do not reproduce Postgres query or
transaction semantics.

Retired `STORE_DRIVER`/`DATA_PATH` values are ignored; old JSON is not read,
imported, or deleted. `TOOL_CONFIG_PATH` remains operator policy, not a Store.
See [storage boundary](../architecture/storage-boundary.md) for fixture separation
and database-test safety.

## Tool Configuration

Missing Tool config defaults to enabled `calculator`, `get_current_time`, and
`web_search`. Web search also needs `TAVILY_API_KEY`, Agent allowlist membership,
and an exact egress/credential policy. New defaults include the rule; existing
custom policies and saved Agent choices are never silently widened.

Enablement is not readiness. Missing credentials expose `credential_unavailable`
and withhold the Tool from new model/Snapshot definitions. Agent lists are
allowlists, not requirements; only explicit `required_tools` makes a missing
capability disqualify a Multi candidate. Losing a frozen prerequisite blocks
Resume. See [Tool security](../tools/tool-security-policy.md).

`TOOL_RESULT_MAX_BATCH_BYTES` and `TOOL_ARTIFACT_*` bound aggregate results,
immutable spills, previews, and persisted expiry; [Artifacts](../tools/tool-result-artifacts.md)
own details. `TOOL_PROGRESS_*` thresholds are frozen per Run; guard rejection
precedes Budget/Handler execution. See [Progress Guard](../tools/tool-progress-guard.md).

## Sandbox Commands

For isolated scratch commands, `SANDBOX_ENABLED` is an operator opt-in; it does
not enable a Tool or grant its capability. Startup checks compatible sbx safety
flags and authentication, and reclaims recorded orphan sandboxes before accepting
work. CPU/RAM are per VM; `SANDBOX_MAX_CONCURRENT` is a separate process-wide VM
cap. Profile changes affect the frozen Tool revision. See
[Sandbox execution](sandbox-execution.md) for the exact authorization rule,
retained-output semantics, supported local deployment and remaining limits.

## Trusted Skill Packages

`TRUSTED_SKILL_DIRS` is a CSV of installation roots, not individual packages.
The template uses `../../.agents/skills` from API cwd. Only immediate reviewed
`SKILL.md` directories are discovered; no recursive scan or package symlinks.
Empty disables new bindings; missing roots, duplicate names, invalid packages,
and catalog overflow fail startup. Exact-package paths fail with a parent-root
hint instead of silently finding zero packages.

Agent profiles bind names from `GET /api/skills`. Roots grant no script, Tool,
or credential permissions. Old Runs reuse bounded frozen content without
rereading disk. See [installation](../tools/skill-installation.md) and
[Loader contract](../tools/trusted-skills.md).

## Verification

Only `completion_contract` opts a new Run in; `VERIFICATION_*` settings bound
execution, not activation. Host commands require trusted local mode, a workspace
root and absolute executable allowlist; OIDC blocks them pending an isolated
runner. Canonical cwd checks and a clean environment do not constitute an OS
sandbox. HTTP requires an exact origin grant, including loopback, with dial-time
DNS checks, no redirects/proxies, a 10s total ceiling and 1 MiB response limit.
Artifact bytes are capped independently with observed-size/hash metadata.
See [Verification](../runtime/verification.md) and
[Execution boundaries](execution-boundaries.md).

## Operational Checklist

Check actual vector dimensions; reindex changed identities; set prices before
cost limits; deliberately scope verifier commands/hosts and CORS/access; and
create a new Run after changing frozen settings. Do not reinterpret old Runs
through current editable config.
