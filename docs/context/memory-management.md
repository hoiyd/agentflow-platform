# Curated Semantic Memory

The design goal is precision before recall: durable memory should contain facts
the user intended to persist, not a second copy of the conversation.

AgentFlow treats conversation history and durable memory as different data layers:

```text
messages            authoritative raw conversation history
memory_candidates   auditable durable-fact proposals
memories            accepted semantic knowledge used for recall
memory_changes      metadata-only correction/deletion audit
```

Completing a Run no longer copies every user and assistant message into `memories`. Assistant output and ordinary conversation remain available through Messages, Replay, and future session-history retrieval.

## Provider Lifecycle and Curation Flow

Memory is exposed through one replaceable lifecycle boundary:

```text
Initialize -> Recall / Propose / Commit / SyncTurn -> Close
```

Agent Runtime depends only on `Recall`; explicit Memory APIs depend on
`Recall`, `Commit`, and the separate `Administration` capability
(`GetMemory` / `MutateMemory`); the post-response path submits `SyncTurn`. Provider
construction, initialization, and shutdown remain in the application
composition root, so changing the provider does not change the Turn Engine.

After the response has been flushed, the user message is submitted to the
built-in provider's background sync worker:

```text
completed response
  -> explicit rule extraction
  -> adaptive model fallback (optional)
  -> conservative policy evaluation
  -> persisted accepted/rejected candidate
  -> accepted candidate embedding
  -> durable semantic memory
```

The rule extractor remains the high-precision fast path. It recognizes explicit English and Chinese signals such as:

- `Remember that ...` / `请记住...`
- `I prefer ...` / `我的偏好是...`
- `Correction: ...` / `更正：...`
- `For this project, ...` / `项目约定：...`

When no rule matches, adaptive extraction can ask the configured chat model for one structured `ADD` or `NOOP` decision. A cheap prefilter skips assistant messages, obvious questions, short or oversized messages, temporary/task-result content, and potential secrets before any auxiliary model call. Model output is constrained to durable facts, preferences, corrections, and project conventions.

The composite extractor always runs rules first, so explicit requests do not spend an additional model request. The original Message remains authoritative evidence and every Candidate stores its `source_message_id`; model-generated text is never treated as evidence by itself.

Adaptive extraction has three modes:

- `off`: use only deterministic rule extraction.
- `shadow`: persist adaptive Candidates as rejected with `policy_reason=adaptive_shadow_mode`, without embedding or adding durable Memory.
- `auto`: allow adaptive Candidates above the confidence threshold to continue through policy and persistence.

`shadow` is the default rollout mode. Adaptive extraction is disabled automatically when `OPENAI_API_KEY` is empty. Promote to `auto` only after reviewing shadow Candidates against representative conversations.

```bash
MEMORY_ADAPTIVE_EXTRACTION_MODE=shadow
MEMORY_ADAPTIVE_MIN_CONFIDENCE=0.85
```

The model supports only `ADD/NOOP`. Explicit users can correct/delete Memory through
versioned mutations below; autonomous consolidation is not enabled. A `Correction:`
message still proposes a new fact rather than implicitly modifying another record.

The deterministic policy rejects low-confidence adaptive proposals, temporary instructions, task-completion logs, oversized content, and potential secrets. Secret-like rejected content is persisted only as `[redacted potential secret]`; the original value is not written to Candidate events or durable Memory.

## Events and Failure Behavior

Candidate decisions and accepted writes use typed Run Events:

```text
memory.candidate.proposed
memory.candidate.accepted | memory.candidate.rejected | memory.candidate.failed
memory.sync.requested
memory.sync.completed | memory.sync.failed
memory.sync.rejected
memory.recall.failed
```

The provider uses a bounded, ordered background queue and drains accepted work
during shutdown. A stable Turn sync key and Candidate ID make duplicate
delivery idempotent across queue attempts and process retries. Transient
provider operations use bounded exponential retries. Adaptive model requests
share the normal model concurrency, rate-limit, retry, and timeout controls.
Extraction, embedding, queue, Candidate, or Memory failures are observable,
but they do not change a successfully completed Run. Recall failure degrades
to an empty Memory set and emits `memory.recall.failed`; ordinary Chat can
therefore continue without silently hiding the auxiliary failure.

The explicit `POST /api/memories` and `POST /api/memories/search` APIs remain
available and are consumed by the **Memory** workspace. Operators can save an
intentional Memory record and inspect semantic Recall results, including score,
similarity, recency boost, kind, and available Run/Conversation provenance.
Manual writes carry `metadata.source=manual_workbench`. The same workspace offers
correction, deletion, and change history for both manual and conversation-derived
Memory. History can also be opened by Memory ID, including a deleted record after
a page reload. Implicit message copying has been replaced with conservative curation.

Memory writes and recall always carry a normalized Workspace namespace. The
provider inherits it from the source Message/Run; explicit HTTP requests resolve
it from the request, and omitted scope becomes `default_workspace`. File and
Postgres search apply that predicate before similarity ranking, so omission is
never an unfiltered recall. User/Project scope, Membership, and Memory-specific
ACL/content-trust policy remain future authorization work.

## Versioned Corrections and Deletions (H-17)

A Memory keeps its stable `id`. New records start at `version=1`; each successful
`replace` or `delete` advances the version exactly once. A correction replaces the
content and embedding together. `previous_version -> version` records the
replacement relation without introducing a second Memory identity or retaining
historical content snapshots.

```http
POST /api/memories/{id}/mutations
X-Workspace-ID: default_workspace
Content-Type: application/json

{
  "operation_id": "a-new-uuid-for-this-correction",
  "expected_version": 1,
  "action": "replace",
  "content": "The release day is Tuesday.",
  "actor": "local-user",
  "reason": "Corrected release schedule"
}
```

For deletion use `action="delete"` and omit `content`. Every command requires an
operation ID, expected version, actor, and reason. Limits are UTF-8 bytes:
operation ID/actor 128, reason 512, replacement content 8,000. These limits apply
after trimming surrounding whitespace. Actor is self-reported audit attribution,
not authenticated identity. Known credential patterns in actor/reason are redacted;
do not put sensitive original text in audit reasons.

- **Concurrency:** stale versions and changed commands reusing an operation ID
  return HTTP `409` / `memory_version_conflict`. The UI preserves the draft but
  requires an explicit refresh before another attempt; no blind retry overwrites
  another correction.
- **Idempotency:** IDs are unique within a Workspace. Resending the same normalized
  command returns `200`, `applied=false`, the original change, and the current
  Memory. A retry never rewinds later changes and needs no embedding call once the
  command is committed. The audit stores a command hash, not the submitted content.
- **Atomicity:** replacement embedding runs before the write transaction. A failed
  embedding leaves content/version unchanged. Persistence atomically updates the
  Memory, vector, candidate suppression, and audit. Deletion needs no model or
  embedding service. Failed writes roll back in Postgres.
- **Deletion:** a tombstone retains identity/version, kind, timestamps and source
  references. Content, metadata and embedding are removed. Recall excludes it;
  `GET /api/memories/{id}` still returns its state and up to 100 newest changes.
  Audit includes actor, reason, action and source reference, not historical text or
  vectors. Tombstones and command receipts currently have no automatic expiry;
  purging them would also discard replay protection and must be coordinated.
- **Late sync:** `CreateMemory` is insert-only, not an upsert. An existing ID with
  changed content/scope/provenance conflicts. Once a source message has a mutation,
  new materializations from that source in the same Workspace are rejected even
  under another Memory ID. Existing and late Candidates in that Workspace are rejected and their content is
  scrubbed. The source fence lives in the audit, so deleting a source conversation
  does not erase it. A worker already holding an accepted Candidate fails its
  commit and emits `memory.sync.failed`; the original Run is not retroactively failed.
- **Scope:** detail, audit and mutation use the same Workspace namespace as Recall.
  Other Workspaces receive `404`. Namespace filtering is not authentication/ACL.

This source-level fence deliberately does not semantically blacklist future user
messages or merge independent Memory records. Correcting one ID cannot find every
semantically equivalent fact elsewhere. Existing context, completed Run captures,
source Messages, external backups and already-running Recall requests are not
retroactively rewritten or erased; their retention remains independently owned.
New Recall sees only the current stored revision.

Postgres startup adds `version`, `deleted_at`, and `memory_changes` idempotently.
Existing rows default to version 1.
Candidates also persist Workspace scope. Existing candidates inherit their source
Conversation's Workspace at migration, or `default_workspace` when no Conversation
remains. Source-based suppression is always Workspace-scoped.
Postgres persists Memory vectors for restart-safe Recall. HTTP Memory responses do not contain vectors.
The built-in Postgres adapter currently serializes Memory writes with one
transaction advisory lock to coordinate late proposals and mutations. Embedding
and Recall do not hold this lock; source-scoped locking is a future throughput
optimization, not a new lifecycle framework.

Focused offline verification (from `apps/api`):

```bash
go test ./internal/store ./internal/memory ./internal/httpapi -run 'Memory|LateMemory' -timeout=3m
# Set TEST_DATABASE_URL to an isolated pgvector database to include Postgres cases.
```

## Trade-offs and Boundaries

- Conservative policy can miss implicit preferences. Shadow mode exists to
  measure those misses before widening persistence.
- Automated extraction remains ADD/NOOP. Explicit replace/delete is supported;
  automatic semantic merge, approvals, history rollback and consolidation are not.
- Model confidence is one input, not authorization. Deterministic policy still
  controls persistence.
- Workspace namespace isolation is not proof of identity or permission to read
  that namespace.
- Memory synchronization is auxiliary platform work. Its failure is observable but
  cannot retroactively fail an already completed Run.
