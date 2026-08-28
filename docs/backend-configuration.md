# Backend Configuration

Configuration is read at process startup, normalized, and passed through the
application composition root. `.env.example` is the executable reference for
local defaults; this document explains ownership and interaction between the
settings.

For the ownership, scope, unit, interaction rules, and tuning order of every
major limit, start with [Execution Controls](execution-controls.md).

## Baseline Environment

Common environment variables:

```bash
BIND_ADDRESS=127.0.0.1
PORT=8080
STORE_DRIVER=file
DATA_PATH=.data/agentflow.json
TOOL_CONFIG_PATH=.data/tools.json
VERIFICATION_WORKSPACE_ROOT=
VERIFICATION_ALLOWED_COMMANDS=
VERIFICATION_ALLOWED_HTTP_HOSTS=
VERIFICATION_MAX_ARTIFACT_BYTES=65536

OPENAI_API_KEY=
OPENAI_BASE_URL=https://api.openai.com/v1
OPENAI_MODEL=gpt-4o-mini
EMBEDDING_BASE_URL=http://localhost:11434/api/embed
EMBEDDING_MODEL=embeddinggemma
EMBEDDING_DIMENSIONS=1536
OPENAI_REQUEST_TIMEOUT=5m

MAX_CONCURRENT_RUNS=8
RUN_QUEUE_SIZE=32
RUN_QUEUE_WAIT_TIMEOUT=30s
MAX_CONCURRENT_CHILD_RUNS=2
MAX_CHILD_RUNS_PER_PARENT=1
CHILD_RUN_TIMEOUT=2m
CHILD_RUN_SUMMARY_MAX_CHARACTERS=4000
CHILD_RUN_MAX_MODEL_CALLS=8
CHILD_RUN_MAX_TOTAL_TOKENS=12000
CHILD_RUN_MAX_TOOL_CALLS=8
MAX_CONCURRENT_MODEL_REQUESTS=8
MODEL_REQUESTS_PER_MINUTE=60
MODEL_TOKENS_PER_MINUTE=120000
MODEL_RETRY_MAX_ATTEMPTS=3
MODEL_RETRY_BASE_DELAY=500ms
MODEL_RETRY_MAX_DELAY=5s
MODEL_REQUEST_CAPTURE_MODE=metadata_only
MODEL_REQUEST_CAPTURE_MAX_BYTES=262144
MODEL_REQUEST_CAPTURE_RETENTION=168h
RUNTIME_INVARIANT_MODE=report

RUN_MAX_MODEL_CALLS=32
RUN_MAX_PROMPT_TOKENS=200000
RUN_MAX_COMPLETION_TOKENS=50000
RUN_MAX_TOTAL_TOKENS=250000
RUN_MAX_TOOL_CALLS=50
RUN_MAX_RUNTIME=15m
RUN_MAX_ESTIMATED_COST_USD=0
MODEL_INPUT_COST_PER_MILLION_TOKENS_USD=0
MODEL_OUTPUT_COST_PER_MILLION_TOKENS_USD=0

ROUTER_MODE=auto
ALLOWED_ORIGINS=http://localhost:3000
```

`BIND_ADDRESS` defaults to loopback because the API has no built-in
authentication. Set it to `0.0.0.0` only behind an intentional external access
boundary. `ALLOWED_ORIGINS` controls browser CORS and does not provide access
control.

## Workspace Scope

Conversation, Message, Run, Document, Memory, and retrieval requests always
carry a non-empty workspace scope. Clients may send `X-Workspace-ID`; when it
is omitted, both API and web client use the reserved `default_workspace`
namespace. Workspace isolation has no feature flag and cannot be disabled.

Existing File Store and Postgres records with an empty workspace or the legacy
reserved value `default` are migrated to `default_workspace`. Explicit custom
workspace IDs remain unchanged.

Workspace scope is an isolation key, not authentication. Production deployments
must derive or validate it at a trusted gateway rather than trusting arbitrary
client-supplied headers.

The web client sends `NEXT_PUBLIC_WORKSPACE_ID` when configured and otherwise
uses `default_workspace`. Changing this value selects a namespace; it does not
grant access to that namespace.

Do not commit `.env` files. Runtime Snapshots freeze provider endpoints and
model identity for reproducibility, but credentials remain live process
configuration and are never persisted with a Run.

## Concurrency, Rate Limits, and Retry

Concurrency settings control different layers:

- `MAX_CONCURRENT_RUNS` limits active Agent runs. Runs for the same conversation remain single-writer.
- `RUN_QUEUE_SIZE` adds bounded waiting capacity beyond active runs. Excess requests receive `429` with `Retry-After`.
- `RUN_QUEUE_WAIT_TIMEOUT` limits queue waiting time. Timed-out requests receive `503` with `Retry-After`.
- `MAX_CONCURRENT_CHILD_RUNS` caps delegated Worker Runs across the process. Child admission never queues.
- `MAX_CHILD_RUNS_PER_PARENT` caps one parent Run's active fan-out; delegation depth is fixed at one.
- `CHILD_RUN_TIMEOUT` and `CHILD_RUN_MAX_*` values are frozen into the Child Runtime Snapshot and enforced by its independent Run Budget.
- `CHILD_RUN_SUMMARY_MAX_CHARACTERS` limits model-visible output returned to the parent; complete output remains in the Child Trace.
- `MAX_CONCURRENT_MODEL_REQUESTS` limits model HTTP requests currently in flight across Chat and Embeddings. It is a request limit, not a model-count or connection-pool setting. Streaming responses hold a slot until the response body closes.
- `MODEL_REQUESTS_PER_MINUTE` is the per-API-key request token-bucket capacity and refill rate.
- `MODEL_TOKENS_PER_MINUTE` is the per-API-key approximate input-token bucket based on serialized request size; streamed output tokens are not included.
- Each retry attempt acquires a new model-request permit and counts toward RPM/TPM. Backoff waits do not hold a concurrency slot.
- `MODEL_RETRY_MAX_ATTEMPTS` includes the initial request. Set it to `1` to disable retries.
- `MODEL_RETRY_BASE_DELAY` starts exponential backoff; `MODEL_RETRY_MAX_DELAY` caps both backoff and provider `Retry-After` values.

Set either per-minute value to `0` to disable that token bucket.

Model errors are classified before retry. Transport failures, timeouts, rate limits, provider `5xx` responses, and invalid provider responses are retryable. Authentication, quota, model-not-found, invalid request, context-length, content-policy, local request token-bucket capacity, and canceled errors fail immediately. Streaming requests retry only before the first output delta, preventing duplicated assistant text.

## Model Request Capture

Every run-scoped physical chat request persists a secret-free Envelope and the
SHA-256 hash of the exact canonical JSON passed to the model transport.
`MODEL_REQUEST_CAPTURE_MODE` controls optional content retention:

- `metadata_only`: no prompt content; production-safe default.
- `redacted`: bounded canonical JSON after deterministic secret redaction.
- `full`: exact bounded JSON for trusted local debugging only.

`MODEL_REQUEST_CAPTURE_MAX_BYTES` applies only to stored redacted/full content.
Oversized content is omitted and marked truncated; hashes, counts, effective
parameters, Runtime Snapshot hash, and Context Manifest reference remain.
`MODEL_REQUEST_CAPTURE_RETENTION` controls content lifetime. Expired content is
purged lazily on File/Postgres reads; durable Envelope and redaction metadata
remain available.
Changing this process-level observability policy does not change model input or
the semantic protocol frozen with a Run. See
[Model request reconstruction](model-request-reconstruction.md).

## Runtime Invariants

`RUNTIME_INVARIANT_MODE` controls projection and Replay diagnostics:

- `report` logs stable invariant codes and returns them in
  `projection.invariant_failures` without changing the Run result;
- `fail` makes projection and Replay reads fail when a violation is present,
  which is useful for local development and CI fixtures.

Unknown values normalize to `report`. Invariant checks never mutate model
requests, Tool results, persisted events, or Run completion state.

## Run Budget

`RUN_MAX_*` values are frozen into each new Run's Runtime Snapshot. Set a call,
token, tool, cost, or runtime value to `0` to disable that dimension. Runtime
means accumulated `running` segments; queue and `waiting_for_user` time are not
charged.

The two model price settings and maximum cost are USD values converted to
integer microdollars for persistence. Cost enforcement is disabled while
`RUN_MAX_ESTIMATED_COST_USD=0`. Configure both input and output prices for the
selected model before enabling it.

Run model calls are logical operations. Provider retries continue to consume
request concurrency and RPM/TPM, but reuse one Run reservation. See
[Run Budget and Usage Ledger](run-budget.md) for purpose scope, settlement,
output caps, Autonomous precedence, and observed-overage semantics.

`AUTONOMOUS_MAX_ITERATIONS` and `AUTONOMOUS_MAX_OUTPUT_CHARS` are mode-owned
loop guards. `AUTONOMOUS_MAX_RUNTIME_SECONDS` and
`AUTONOMOUS_MAX_TOOL_CALLS` are mode-specific configuration caps: for new Runs
they are folded into the frozen Run Budget by taking the stricter value, then
only Run Budget enforces those two resources. This avoids competing counters
while preserving the existing Autonomous safety profile.

## Model and Embedding Providers

If `OPENAI_API_KEY` is empty, chat uses a deterministic local fallback for
workflow exercises. Embeddings call Ollama when `EMBEDDING_BASE_URL` points to
`http://localhost:11434/api/embed`; otherwise they use deterministic local
fallback. The frontend search panel shows whether RAG search used
`ollama / <model>`, `local / local_hash_embedding`, or an OpenAI-compatible
embedding provider.

The local fallback is intended to verify workflows and persistence without
provider cost. It is not a substitute for evaluating model quality or semantic
retrieval quality.

To split providers, keep chat on a hosted OpenAI-compatible API and point embeddings to local Ollama:

```bash
OPENAI_BASE_URL=https://api.openai.com/v1
EMBEDDING_BASE_URL=http://localhost:11434/api/embed
EMBEDDING_MODEL=embeddinggemma
```

Ollama's `/api/embed` endpoint is supported directly. To use an OpenAI-compatible embedding provider instead, set `EMBEDDING_BASE_URL` to that provider's `/v1` base URL and set `EMBEDDING_MODEL` accordingly.

Ollama embedding dimensions depend on the selected model. The bundled Postgres schema currently uses `vector(1536)`, so use a 1536-dimensional Ollama embedding model with Postgres, or migrate the vector columns to the model's actual dimension.

To use a stronger embedding model without changing the existing `vector(1536)` pgvector schema:

```bash
EMBEDDING_MODEL=text-embedding-3-large
EMBEDDING_DIMENSIONS=1536
```

After changing embedding model/provider, re-upload or reindex documents. Search filters candidates by embedding provider/model so old chunks are not mixed with the new query vector space.

## Adaptive Memory Extraction

The Memory Curator always recognizes explicit durability signals through deterministic rules. Optional adaptive extraction runs only after the rule path returns no Candidate:

```bash
MEMORY_ADAPTIVE_EXTRACTION_MODE=shadow
MEMORY_ADAPTIVE_MIN_CONFIDENCE=0.85
```

Modes are `off`, `shadow`, and `auto`. `shadow` records model proposals for evaluation but does not commit them to durable Memory. `auto` commits only proposals that pass the confidence threshold and the deterministic safety policy. No adaptive model request is made when `OPENAI_API_KEY` is empty.

Adaptive extraction uses the configured chat model and therefore consumes the same global concurrency, RPM, and TPM budgets as normal model requests. Explicit rule matches remain model-free.

## Postgres + pgvector

By default the backend uses the local file store. To use Postgres:

```bash
STORE_DRIVER=postgres
DATABASE_URL=postgres://agentflow:agentflow@localhost:5432/agentflow?sslmode=disable
```

The Postgres store runs idempotent startup migrations for:

- conversations, messages, Agents, Runs, Collaboration Steps, and durable Run Events
- Run active-runtime state and append-only usage entries
- Model Request Envelopes and optional Context Captures
- memory candidates, curated memories, and `memory_embeddings`
- documents, document chunks, and document chunk embeddings
- pgvector HNSW indexes for semantic search
- generated `tsvector` columns and GIN indexes for document and chunk lexical search

Lexical search does not require an additional environment variable. The
Postgres startup migration creates and maintains the generated full-text
columns automatically. The file store performs the same logical recall path
with in-process phrase, identifier, and term-coverage scoring instead of a
persisted text index.

## Tool Configuration

The backend loads enabled tools from `TOOL_CONFIG_PATH`, defaulting to `.data/tools.json`. If the file is missing, all built-in tools are enabled.

```json
{
  "enabled_tools": [
    "calculator",
    "get_current_time"
  ]
}
```

The tool executor applies typed errors, per-tool timeouts, result-size limits,
and typed Run Events to every call.

## Verification

Verification is enabled per Run by including `completion_contract` in the
initial `POST /api/chat` request. These environment variables only define
verifier security boundaries and output limits; configuring them does not
automatically verify Single, Multi-Agent, or Autonomous chats.

The command verifier is disabled when `VERIFICATION_WORKSPACE_ROOT` or
`VERIFICATION_ALLOWED_COMMANDS` is empty. The command is an argument vector
executed without a shell; its relative working directory cannot escape the
configured root. `VERIFICATION_ALLOWED_COMMANDS` is a comma-separated exact
executable allowlist.

The HTTP verifier permits localhost and loopback IPs.
`VERIFICATION_ALLOWED_HTTP_HOSTS` adds comma-separated exact hostname or
host:port values. Redirects follow the same allowlist.
`VERIFICATION_MAX_ARTIFACT_BYTES` caps persisted output for each verifier while
the Artifact keeps the output hash, observed byte count, and truncation flag.

See [Verification](verification.md) for contract and Gate behavior.

## Operational Checklist

Before sharing or deploying a configuration:

1. Verify the selected embedding dimension matches the persisted pgvector
   column dimension.
2. Reindex documents after changing embedding provider, model, or dimension.
3. Enable cost enforcement only after configuring prices for the active model.
4. Keep the command verifier disabled unless its workspace root and executable
   allowlist are intentionally scoped.
5. Confirm allowed origins and HTTP verifier hosts are explicit for the target
   environment.
6. Create a new Run after changing frozen settings; existing Runs retain their
   captured protocol.
