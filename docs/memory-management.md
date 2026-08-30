# Curated Semantic Memory

The design goal is precision before recall: durable memory should contain facts
the user intended to persist, not a second copy of the conversation.

AgentFlow treats conversation history and durable memory as different data layers:

```text
messages            authoritative raw conversation history
memory_candidates   auditable durable-fact proposals
memories            accepted semantic knowledge used for recall
```

Completing a Run no longer copies every user and assistant message into `memories`. Assistant output and ordinary conversation remain available through Messages, Replay, and future session-history retrieval.

## Curation Flow

After the response has been flushed, the user message is submitted to the background Memory Curator:

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

The model currently supports only `ADD/NOOP`. It does not replace or remove existing Memory because safe consolidation requires retrieval of related memories and versioned mutation semantics.

The deterministic policy rejects low-confidence adaptive proposals, temporary instructions, task-completion logs, oversized content, and potential secrets. Secret-like rejected content is persisted only as `[redacted potential secret]`; the original value is not written to Candidate events or durable Memory.

## Events and Failure Behavior

Candidate decisions and accepted writes use typed Run Events:

```text
memory.candidate.proposed
memory.candidate.accepted | memory.candidate.rejected | memory.candidate.failed
memory.sync.requested
memory.sync.completed | memory.sync.failed
```

The Curator uses a bounded, ordered background queue and drains accepted work during shutdown. Adaptive model requests share the normal model concurrency, rate-limit, retry, and timeout controls. Extraction, embedding, queue, Candidate, or Memory failures are observable, but they do not change a successfully completed Run.

The explicit `POST /api/memories` and `POST /api/memories/search` APIs remain
available and are consumed by the **Memory** workspace. Operators can save an
intentional Memory record and inspect semantic Recall results, including score,
similarity, recency boost, kind, and available Run/Conversation provenance.
Manual writes carry `metadata.source=manual_workbench`. Versioned
replace/remove mutations are not implemented; implicit message copying has
been replaced with conservative curation.

Memory writes and recall always carry a normalized Workspace namespace. The
Curator inherits it from the source Message/Run; explicit HTTP requests resolve
it from the request, and omitted scope becomes `default_workspace`. File and
Postgres search apply that predicate before similarity ranking, so omission is
never an unfiltered recall. User/Project scope, Membership, and Memory-specific
ACL/content-trust policy remain future authorization work.

## Trade-offs and Boundaries

- Conservative policy can miss implicit preferences. Shadow mode exists to
  measure those misses before widening persistence.
- Accepted Memory is append-only in the current protocol. Replace, merge, and
  delete require versioned mutation semantics and related-memory retrieval.
- Model confidence is one input, not authorization. Deterministic policy still
  controls persistence.
- Workspace namespace isolation is not proof of identity or permission to read
  that namespace.
- Memory Curation is auxiliary platform work. Its failure is observable but
  cannot retroactively fail an already completed Run.
