# AgentFlow Platform

Full-stack AI agent workflow platform with streaming chat, agent runs, persistent memory, tool execution, replay traces, and a minimal RAG knowledge base.

## Current Capabilities

- Chat UI with persisted conversations and streamed assistant responses.
- Single-agent, multi-agent, and autonomous run modes.
- Run lifecycle tracking with collaboration steps, trace events, replay, and cancel/resume/continue flows.
- Built-in and MCP tool registry with enable/disable controls.
- Persistent semantic memory backed by embeddings.
- RAG knowledge base with text, `.txt`, `.md`, and `.markdown` ingestion.
- Markdown-aware chunking with heading, list, paragraph, and fenced-code metadata.
- pgvector/Postgres support for memory and document chunk similarity search.
- Frontend Knowledge page for document upload, document detail, search, similarity threshold, and deletion.
- Search rerank on the backend using lexical match, metadata match, recency, and simple diversity control.

## Tech Stack

- Frontend: Next.js, React, TypeScript.
- Backend: Go.
- Storage: local JSON file store by default, optional Postgres with pgvector.
- AI: OpenAI-compatible chat and embeddings APIs.
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
OPENAI_EMBEDDING_MODEL=text-embedding-3-small
OPENAI_EMBEDDING_DIMENSIONS=1536
OPENAI_REQUEST_TIMEOUT=5m

ROUTER_MODE=auto
ALLOWED_ORIGINS=http://localhost:3000
```

If `OPENAI_API_KEY` is empty, chat and embeddings use deterministic local fallbacks for verification. The frontend search panel shows whether RAG search used `local / local_hash_embedding` or an OpenAI-compatible embedding provider.

To use a stronger embedding model without changing the existing `vector(1536)` pgvector schema:

```bash
OPENAI_EMBEDDING_MODEL=text-embedding-3-large
OPENAI_EMBEDDING_DIMENSIONS=1536
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
2. Ask a chat question that should use that phrase.
3. Open the run replay page from the active run link.
4. Confirm the Retrieved context panel shows retrieval event count, memory count, chunk count, and embedding provider/model/dimensions.
5. Select a `retrieval` or `llm_start` event.
6. Confirm retrieved memories and knowledge chunks are visible above the raw JSON payload.

## Tool Configuration

The backend loads enabled tools from `TOOL_CONFIG_PATH`, defaulting to `.data/tools.json`. If the file is missing, all built-in tools are enabled.

```json
{
  "enabled_tools": [
    "calculator",
    "smartapis__smartagent_discovery_capabilities",
    "smartapis__smartagent_catalog_list_plans",
    "smartapis__smartagent_places_search"
  ],
  "mcp_servers": [
    {
      "id": "smartapis",
      "enabled": true,
      "transport": "streamable-http",
      "url": "https://smartapis.net/mcp"
    }
  ]
}
```

MCP tools are registered as `<server_id>__<tool_name>` to avoid collisions with built-in tools. Stdio MCP servers are also supported with `transport`, `command`, and `args`.
