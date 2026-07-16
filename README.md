# AgentFlow Platform

Full-stack AI agent workflow platform with streaming chat, agent runs, persistent memory, tool execution, replay traces, and a minimal RAG knowledge base.

## Current Capabilities

- Chat UI with persisted conversations and streamed assistant responses.
- Single-agent, multi-agent, and autonomous run modes.
- Run lifecycle tracking with collaboration steps, trace events, replay, and cancel/resume/continue flows.
- Built-in tool catalog with enable/disable controls and guarded execution.
- Curated semantic memory backed by embeddings; ordinary chat and assistant output remain conversation history.
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

## Quick Start

### Backend

```bash
cd apps/api
cp .env.example .env
gvm use go1.25.5
GOCACHE=/private/tmp/agentflow-go-build-cache go run ./cmd/server
```

The API runs at `http://localhost:8080` by default.

### Frontend

```bash
cd apps/web
npm install
npm run dev
```

The web app runs at `http://localhost:3000`.

Set `NEXT_PUBLIC_API_BASE_URL` only if the API is not running on `http://localhost:8080`.

## Documentation

- [Backend Architecture](docs/backend-architecture.md): Go package ownership, dependency direction, application composition, and lifecycle.
- [Backend Configuration](docs/backend-configuration.md): environment variables, concurrency, retry policy, model and embedding providers, Postgres, and tool configuration.
- [Context Management](docs/context-management.md): context assembly, compaction triggers, algorithm, compression ratio, persistence, and failure behavior.
- [Memory Management](docs/memory-management.md): hybrid candidate extraction, shadow evaluation, curation policy, background persistence, and events.
- [Knowledge / RAG](docs/knowledge-rag.md): document ingestion, Markdown chunking, vector search, and reranking.
- [API Reference](docs/api-reference.md): HTTP endpoints and an example RAG response.
- [Verification Guide](docs/verification-guide.md): backend tests, frontend build, RAG checks, and replay checks.
