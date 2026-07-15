# AgentFlow Platform

Full-stack AI agent workflow platform with streaming chat, agent runs, persistent memory, tool execution, replay traces, and a minimal RAG knowledge base.

## Current Capabilities

- Chat UI with persisted conversations and streamed assistant responses.
- Single-agent, multi-agent, and autonomous run modes.
- Run lifecycle tracking with collaboration steps, trace events, replay, and cancel/resume/continue flows.
- Built-in tool catalog with enable/disable controls and guarded execution.
- Persistent semantic memory backed by embeddings.
- RAG knowledge base with text, `.txt`, `.md`, and `.markdown` ingestion.
- Markdown-aware chunking with heading, list, paragraph, and fenced-code metadata.
- pgvector/Postgres support for memory and document chunk similarity search.
- Frontend Knowledge page for document upload, document detail, search, similarity threshold, and deletion.
- Search rerank on the backend using lexical match, metadata match, recency, and simple diversity control.
- Optional LangChainGo executor adapter for single-agent chat steps while keeping native orchestration as the default.

## Tech Stack

- Frontend: Next.js, React, TypeScript.
- Backend: Go.
- Storage: local JSON file store by default, optional Postgres with pgvector.
- AI: OpenAI-compatible chat and embeddings APIs.
- Agent framework adapter: LangChainGo for optional step-level execution.
- Embeddings: default `text-embedding-3-small`, configurable to `text-embedding-3-large` with fixed dimensions.

## Project Structure

```txt
agentflow-platform/
  apps/
    api/      # Go backend
    web/      # Next.js frontend
  packages/
    shared/   # shared contracts placeholder
```

## Backend

```bash
cd apps/api
cp .env.example .env
gvm use go1.25.5
GOCACHE=/private/tmp/agentflow-go-build-cache go run ./cmd/server
```

The API runs at `http://localhost:8080` by default.

### Backend Configuration

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

### Postgres + pgvector

By default the backend uses the local file store. To use Postgres:

```bash
STORE_DRIVER=postgres
DATABASE_URL=postgres://agentflow:agentflow@localhost:5432/agentflow?sslmode=disable
```

The Postgres store runs idempotent startup migrations for:

- conversations, messages, agents, runs, collaboration steps, and trace events
- memories and `memory_embeddings`
- documents, document chunks, and document chunk embeddings
- pgvector HNSW indexes for semantic search

## Frontend

```bash
cd apps/web
npm install
npm run dev
```

The web app runs at `http://localhost:3000`.

Set `NEXT_PUBLIC_API_BASE_URL` only if the API is not running on `http://localhost:8080`.

## Knowledge / RAG

The Knowledge page supports:

- adding a text document from a textarea
- uploading `.txt`, `.md`, and `.markdown` files
- listing documents with title, filename, format, chunk count, and embedding count
- viewing document chunks and Markdown metadata
- deleting documents and their chunks/embeddings
- searching indexed chunks with a minimum similarity threshold
- seeing embedding provider/model/dimensions used for search

Markdown ingestion is structure-aware:

- headings become `heading_path`
- paragraphs, lists, and fenced code blocks become chunk types
- code block language is preserved when available
- oversized blocks fall back to fixed-size chunk splitting
- heading context is included in chunk content to improve retrieval

Search flow:

1. Query is embedded with the configured embedding provider/model.
2. Vector search retrieves a larger candidate set.
3. Backend rerank applies lexical boost, metadata boost, recency, and diversity control.
4. Results return similarity, score, rerank score, and boost components.

## API Summary

```txt
GET    /health

GET    /api/conversations
POST   /api/conversations
DELETE /api/conversations/{id}
GET    /api/conversations/{id}/messages

POST   /api/chat

GET    /api/agents
POST   /api/agents
GET    /api/agents/{id}

GET    /api/runs
GET    /api/runs/{id}
POST   /api/runs/{id}/continue
POST   /api/runs/{id}/resume
POST   /api/runs/{id}/cancel
GET    /api/runs/{id}/collaboration_steps
GET    /api/runs/{id}/replay

GET    /api/tools
POST   /api/tools/{name}/enable
POST   /api/tools/{name}/disable

POST   /api/memories
POST   /api/memories/search

GET    /api/documents
POST   /api/documents
POST   /api/documents/upload
GET    /api/documents/{id}
DELETE /api/documents/{id}
POST   /api/rag/search
```

Example RAG search response:

```json
{
  "items": [
    {
      "similarity": 0.71,
      "recency_boost": 0.03,
      "score": 0.74,
      "lexical_boost": 0.18,
      "metadata_boost": 0.1,
      "rerank_score": 1.02
    }
  ],
  "embedding": {
    "provider": "openai_compatible",
    "model": "text-embedding-3-large",
    "dimensions": 1536,
    "estimated": false
  }
}
```

## Manual Verification

Backend tests:

```bash
cd apps/api
source ~/.gvm/scripts/gvm
gvm use go1.25.5 >/dev/null
mkdir -p /private/tmp/agentflow-go-build-cache
GOCACHE=/private/tmp/agentflow-go-build-cache go test ./...
```

Frontend build:

```bash
cd apps/web
export PATH="$HOME/.nvm/versions/node/v22.6.0/bin:$PATH"
npm run build
```

RAG manual flow:

1. Start backend and frontend.
2. Open the Knowledge page.
3. Upload a `.txt` or `.md` file with a unique phrase.
4. Confirm the document list shows chunk and embedding counts.
5. Click Details and inspect chunks/metadata.
6. Search for the unique phrase.
7. Confirm the search panel shows embedding provider/model/dimensions.
8. Confirm relevant chunks rank above unrelated content.
9. Delete the document and confirm it disappears from list/search.

Demo replay flow:

1. Add or upload a knowledge document with a unique phrase.
2. Ask a chat question that should use that phrase. In Single Agent mode, optionally switch Executor from Native to LangChainGo.
3. Open the run replay page from the active run link.
4. Confirm the Retrieved context panel shows retrieval event count, memory count, chunk count, embedding provider/model/dimensions, and executor/framework.
5. Select a `retrieval` or `llm_start` event.
6. Confirm retrieved memories and knowledge chunks are visible above the raw JSON payload.
7. For LangChainGo runs, confirm event detail or raw payload includes `executor: "langchaingo"`, `framework: "langchaingo"`, and `framework_path: "chains.LLMChain"`.

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
