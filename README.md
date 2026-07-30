# AgentFlow Platform

Full-stack AI agent workflow platform with streaming chat, agent runs, persistent memory, tool execution, replay traces, and a minimal RAG knowledge base.

## Current Capabilities

- Chat UI with persisted conversations and streamed assistant responses.
- Single-agent, multi-agent, and autonomous run modes.
- Run lifecycle tracking with collaboration steps, trace events, replay, and cancel/resume/continue flows.
- Frozen per-Run budgets with idempotent model/tool usage ledgers and active-runtime accounting.
- Opt-in evidence-gated completion with per-Run frozen contracts, deterministic verifiers, Subject Hash freshness, and Replay artifacts.
- Built-in tool catalog with enable/disable controls and guarded execution.
- Curated semantic memory backed by embeddings; ordinary chat and assistant output remain conversation history.
- RAG knowledge base with text, `.txt`, `.md`, and `.markdown` ingestion.
- Markdown-aware chunking with heading, list, paragraph, and fenced-code metadata.
- Hybrid document retrieval with independent dense and lexical recall for both the local file store and Postgres/pgvector.
- Frontend Knowledge page for document upload, document detail, search, similarity threshold, and deletion.
- Shared RAG pipeline across HTTP, Single-Agent, Multi-Agent, and Autonomous modes, with reciprocal-rank fusion, evidence-aware reranking, and a relevance gate.
- Parent-child context selection that retrieves with child chunks, then fills same-section or adjacent chunks within the knowledge context token limit.
- Context transformation that removes duplicate sources, groups results by document, and merges adjacent chunks without exceeding the knowledge context token limit.
- RAG prompt-injection guard with untrusted-context boundaries, deterministic blocking, and auditable filtering reasons.
- Optional LangChainGo executor adapter for single-agent chat steps while keeping native orchestration as the default.

## Tech Stack

- Frontend: Next.js, React, TypeScript.
- Backend: Go 1.26.5.
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
gvm use go1.26.5
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

- [Execution Controls](docs/execution-controls.md): canonical map of concurrency, rate limits, retries, Run Budget, Context capacity, Autonomous guards, Tool and Verification limits.
- [Backend Architecture](docs/backend-architecture.md): Go package ownership, dependency direction, application composition, and lifecycle.
- [Backend Configuration](docs/backend-configuration.md): environment variables, concurrency, retry policy, model and embedding providers, Postgres, and tool configuration.
- [Run Budget and Usage Ledger](docs/run-budget.md): frozen limits, accounting scope, hard/observed enforcement, persistence, and API behavior.
- [Context Management](docs/context-management.md): context assembly, compaction triggers, algorithm, compression ratio, persistence, and failure behavior.
- [Completion Verification](docs/completion-verification.md): frozen contracts, deterministic evidence, Subject Hash freshness, completion policy, and security boundaries.
- [Memory Management](docs/memory-management.md): hybrid candidate extraction, shadow evaluation, curation policy, background persistence, and events.
- [Knowledge / RAG](docs/knowledge-rag.md): document ingestion, Markdown chunking, dense and lexical recall, reranking, and relevance gating.
- [API Reference](docs/api-reference.md): HTTP endpoints and an example RAG response.
- [Verification Guide](docs/verification-guide.md): backend tests, frontend build, RAG checks, and replay checks.
