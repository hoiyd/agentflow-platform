# Curated Semantic Memory

AgentFlow treats conversation history and durable memory as different data layers:

```text
messages            authoritative raw conversation history
memory_candidates   auditable durable-fact proposals
memories            accepted semantic knowledge used for recall
```

Completing a Run no longer copies every user and assistant message into `memories`. Assistant output and ordinary conversation remain available through Messages, Replay, and future session-history retrieval.

## Curation flow

After the response has been flushed, the user message is submitted to the background Memory Curator:

```text
completed response
  -> explicit durability signal extraction
  -> conservative policy evaluation
  -> persisted accepted/rejected candidate
  -> accepted candidate embedding
  -> durable semantic memory
```

The initial extractor intentionally favors precision over recall. It recognizes explicit English and Chinese signals such as:

- `Remember that ...` / `请记住...`
- `I prefer ...` / `我的偏好是...`
- `Correction: ...` / `更正：...`
- `For this project, ...` / `项目约定：...`

Messages without one of these signals do not create a Candidate. Assistant messages are never submitted for automatic curation.

The initial policy rejects temporary instructions, task-completion logs, oversized content, and potential secrets. Secret-like rejected content is persisted only as `[redacted potential secret]`; the original value is not written to Candidate events or durable Memory.

## Events and failure behavior

Candidate decisions and accepted writes use typed Run Events:

```text
memory.candidate.proposed
memory.candidate.accepted | memory.candidate.rejected | memory.candidate.failed
memory.sync.requested
memory.sync.completed | memory.sync.failed
```

The Curator uses a bounded, ordered background queue and drains accepted work during shutdown. Extraction, embedding, queue, Candidate, or Memory failures are observable, but they do not change a successfully completed Run.

The explicit `POST /api/memories` and `POST /api/memories/search` APIs remain available. Versioned replace/remove mutations are a later feature; this change only replaces implicit message copying with conservative curation.

## Legacy message-memory cleanup

Older AgentFlow versions created `kind=message` Memory records for every user and assistant message. The migration command identifies only those legacy records by kind plus `source_message_id` or the old `mem_msg_` ID prefix. Curated and explicitly created Memory records are not selected.

Run a dry-run first:

```bash
cd apps/api
go run ./cmd/migrate-curated-memory
```

The command prints a JSON report containing mode, found count, deleted count, and candidate Memory IDs. It does not print Memory content.

Apply the cleanup explicitly:

```bash
go run ./cmd/migrate-curated-memory --apply
```

It uses `STORE_DRIVER`, `DATA_PATH`, and `DATABASE_URL` from the normal backend configuration. Flags can override them:

```bash
go run ./cmd/migrate-curated-memory \
  --store-driver=file \
  --data-path=.data/agentflow.json \
  --apply
```

The cleanup is idempotent: a second apply reports zero matching records. The command does not delete Messages, Candidates, curated Memory, or explicitly created non-message Memory.
