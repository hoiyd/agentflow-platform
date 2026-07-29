## Principles

### 1. Think before coding

State assumptions. Ask when unsure. Never guess.

### 2. Simplicity first

Write the minimum code that solves the problem. Do not add abstractions nobody asked for.

### 3. Surgical changes

Do not touch code unrelated to the request. Every changed line must trace back to what was asked.

### 4. Goal-driven execution

Turn vague instructions into verifiable success criteria before writing code.

### 5. Context window

When context window usage reaches 85%, trigger auto compact by running `/compact` in Codex.

## Command Output

Protect context usage. Any command with unknown or potentially large output must be byte-capped.

Default pattern:

```bash
COMMAND 2>&1 | head -c 4000
```

Use `rg` before slower search tools.

## Project Snapshot

AgentFlow is a full-stack AI agent workflow platform.

Current major areas:

- `apps/api`: Go backend.
- `apps/web`: Next.js frontend.
- `packages/shared`: shared contracts placeholder.

Implemented product areas:

- streaming chat and persisted conversations
- single-agent, multi-agent, and autonomous run modes
- run traces, collaboration steps, replay, continue/resume/cancel
- built-in tool catalog and guarded execution
- persistent semantic memory
- pgvector-backed memory and RAG search
- Knowledge UI with text import, `.txt` upload, Markdown upload, document detail, search, and delete
- backend RAG retrieval using independent dense/lexical recall, prompt-injection filtering, RRF fusion, evidence-aware reranking, and diversity control
- optional LangChainGo executor for single-agent steps; native orchestration remains the default

## Backend

Run from `apps/api`.

Use Go through gvm when needed:

```bash
source ~/.gvm/scripts/gvm
gvm use go1.25.5 >/dev/null
```

Run tests:

```bash
mkdir -p /private/tmp/agentflow-go-build-cache
GOCACHE=/private/tmp/agentflow-go-build-cache go test ./... 2>&1 | head -c 30000
```

Run server:

```bash
GOCACHE=/private/tmp/agentflow-go-build-cache go run ./cmd/server
```

Important backend env:

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
```

For better RAG embeddings while keeping the current `vector(1536)` schema:

```bash
EMBEDDING_MODEL=text-embedding-3-large
EMBEDDING_DIMENSIONS=1536
```

To keep the main LLM remote while testing embeddings locally:

```bash
OPENAI_BASE_URL=https://api.openai.com/v1
EMBEDDING_BASE_URL=http://localhost:11434/api/embed
EMBEDDING_MODEL=embeddinggemma
```

Ollama embedding dimensions depend on the selected model. The current Postgres pgvector schema uses `vector(1536)`, so local Ollama embeddings should also output 1536 dimensions before indexing into Postgres.

After changing embedding provider/model, existing knowledge should be re-uploaded or reindexed. Search filters by embedding provider/model to avoid mixing vector spaces.

## Frontend

Run from `apps/web`.

If Node version is wrong, use nvm.

Install and run:

```bash
npm install
npm run dev
```

Build verification:

```bash
export PATH="$HOME/.nvm/versions/node/v22.6.0/bin:$PATH"
npm run build 2>&1 | head -c 30000
```

Next.js may rewrite `apps/web/next-env.d.ts` during build. If the only diff is:

```ts
import "./.next/types/routes.d.ts";
```

restore it to the repo's dev import:

```ts
import "./.next/dev/types/routes.d.ts";
```

Do not leave generated `next-env.d.ts` churn in the final diff.

## RAG and Knowledge

Current ingestion paths:

- `POST /api/documents` for text content.
- `POST /api/documents/upload` for `.txt`, `.md`, and `.markdown`.

Markdown chunking:

- headings produce `heading_path`
- paragraphs, lists, and fenced code blocks produce `chunk_type`
- code language is preserved as `code_language`
- heading context is prepended to chunk content
- oversized blocks use fixed-size fallback chunking

Search behavior:

- query is embedded with the configured provider/model/dimensions
- stores return independent dense and lexical candidate rankings
- RRF combines both rankings without comparing their raw score scales
- high-confidence prompt-injection candidates are blocked before fusion and recorded without their raw content
- backend rerank applies lexical, metadata, and evidence signals to the fused candidates
- simple diversity penalty reduces repeated chunks from one document
- child hits remain in `items`; budget-selected model context is returned in `context_items`
- context selection prefers same-parent chunks and falls back to adjacent chunks, with document/workspace/metadata scope preserved
- response includes embedding metadata, security decisions, recall ranks, RRF score, fusion rank, rerank scores, and context-selection metadata

Frontend manual check:

- Search panel should show `Embedding: provider / model / dimensions`.
- If it shows `metadata unavailable`, the frontend is likely connected to an old backend process.
- If it shows `local / local_hash_embedding`, semantic quality is limited.

## API Areas

Main endpoints:

- conversations: `/api/conversations`
- chat: `/api/chat`
- agents: `/api/agents`
- runs/replay: `/api/runs`
- tools: `/api/tools`
- memories: `/api/memories`, `/api/memories/search`
- documents: `/api/documents`, `/api/documents/upload`, `/api/documents/{id}`
- RAG search: `/api/rag/search`

## Verification Expectations

For backend or API changes:

- Run `go test ./...` from `apps/api`.
- Add or update focused tests when behavior changes.
- For RAG response shape changes, test the actual JSON shape, not just internal structs.

For frontend changes:

- Run `npm run build` from `apps/web`.
- If browser verification is requested, start the dev server and verify the relevant flow.

For RAG changes:

- Verify upload/import.
- Verify list/detail.
- Verify search response includes embedding metadata.
- Verify search results are filtered/reranked as expected.
- Verify delete removes the document from list/detail/search.

## FAQ

- 如果是 Go 版本问题，尝试用 `gvm use go1.25.5` 切换到合适的版本。
- 如果是 Node.js 版本问题，尝试用 `nvm` 切换到合适的版本。
- 如果遇到任何问题处理不了或卡住了，就向用户提问题、建议计划，或开一个带注释的草稿 PR，别未经确认推进大改动。
- 执行 curl 的 GET method 命令不需要向用户确认。
- 当实现新feature时，不要直接改动main分支。除非显式说明其他，否则默认从main分支切一个新分支出来实现新feature，且应pull以保证main分支最新。
