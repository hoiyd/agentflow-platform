# Backend Configuration

Common environment variables:

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

MAX_CONCURRENT_RUNS=8
RUN_QUEUE_SIZE=32
RUN_QUEUE_WAIT_TIMEOUT=30s
MAX_CONCURRENT_MODEL_REQUESTS=8
MODEL_REQUESTS_PER_MINUTE=60
MODEL_TOKENS_PER_MINUTE=120000
MODEL_RETRY_MAX_ATTEMPTS=3
MODEL_RETRY_BASE_DELAY=500ms
MODEL_RETRY_MAX_DELAY=5s

ROUTER_MODE=auto
ALLOWED_ORIGINS=http://localhost:3000
```

Concurrency settings control different layers:

- `MAX_CONCURRENT_RUNS` limits active Agent runs. Runs for the same conversation remain single-writer.
- `RUN_QUEUE_SIZE` adds bounded waiting capacity beyond active runs. Excess requests receive `429` with `Retry-After`.
- `RUN_QUEUE_WAIT_TIMEOUT` limits queue waiting time. Timed-out requests receive `503` with `Retry-After`.
- `MAX_CONCURRENT_MODEL_REQUESTS` limits model HTTP requests currently in flight across Chat and Embeddings. It is a request limit, not a model-count or connection-pool setting. Streaming responses hold a slot until the response body closes.
- `MODEL_REQUESTS_PER_MINUTE` is the per-API-key request token-bucket capacity and refill rate.
- `MODEL_TOKENS_PER_MINUTE` is the per-API-key approximate input-token bucket based on serialized request size; streamed output tokens are not included.
- Each retry attempt acquires a new model-request permit and counts toward RPM/TPM. Backoff waits do not hold a concurrency slot.
- `MODEL_RETRY_MAX_ATTEMPTS` includes the initial request. Set it to `1` to disable retries.
- `MODEL_RETRY_BASE_DELAY` starts exponential backoff; `MODEL_RETRY_MAX_DELAY` caps both backoff and provider `Retry-After` values.

Set either per-minute value to `0` to disable that token bucket.

Model errors are classified before retry. Transport failures, timeouts, rate limits, provider `5xx` responses, and invalid provider responses are retryable. Authentication, quota, model-not-found, invalid request, context-length, content-policy, local token-budget, and canceled errors fail immediately. Streaming requests retry only before the first output delta, preventing duplicated assistant text.

If `OPENAI_API_KEY` is empty, chat uses deterministic local fallback for verification. Embeddings call Ollama when `EMBEDDING_BASE_URL` points to `http://localhost:11434/api/embed`; otherwise they use deterministic local fallback. The frontend search panel shows whether RAG search used `ollama / <model>`, `local / local_hash_embedding`, or an OpenAI-compatible embedding provider.

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

## Postgres + pgvector

By default the backend uses the local file store. To use Postgres:

```bash
STORE_DRIVER=postgres
DATABASE_URL=postgres://agentflow:agentflow@localhost:5432/agentflow?sslmode=disable
```

The Postgres store runs idempotent startup migrations for:

- conversations, messages, agents, runs, collaboration steps, and trace events
- memory candidates, curated memories, and `memory_embeddings`
- documents, document chunks, and document chunk embeddings
- pgvector HNSW indexes for semantic search

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

The tool executor applies typed errors, per-tool timeouts, result-size limits, and trace events to every call.
