# HTTP API Reference

The API exposes persisted conversations, Agent execution, operational replay,
tools, semantic memory, and RAG knowledge. Streaming chat uses Server-Sent
Events (SSE); resource and inspection endpoints use JSON.

The current API does not implement authentication or authorization. It is
intended for local development or deployment behind a trusted access boundary;
the default `BIND_ADDRESS=127.0.0.1` keeps it local. `ALLOWED_ORIGINS` is a
browser CORS policy, not an authentication mechanism.

All `/api/*` requests accept `X-Workspace-ID`. When omitted, the request uses
the reserved `default_workspace` namespace, so workspace isolation remains
active for every request. The legacy value `default` is normalized to the same
namespace. Header, `workspace_id` query, and JSON payload selectors must agree
when more than one is supplied; conflicting explicit values return `400`.
Conversation, Message, Run, Document, Memory, RAG, Replay, Usage, Episode, and
Verification operations are scoped to the resolved Workspace.
For deployments with user-controlled workspace selection, this header must be
validated by a trusted boundary because it is not an identity credential by
itself.

For lifecycle terminology, read [Internal terms](../architecture/terms.md). For configuration
and security boundaries, read [Backend configuration](../operations/backend-configuration.md).

## Endpoint Map

```txt
GET    /health

GET    /api/conversations
POST   /api/conversations
PATCH  /api/conversations/{id}
DELETE /api/conversations/{id}
GET    /api/conversations/{id}/messages
GET    /api/conversations/{id}/task-state
PATCH  /api/conversations/{id}/task-state
GET    /api/conversations/{id}/task-state/revisions
GET    /api/conversations/{id}/task-state/revisions/{version}

POST   /api/chat

GET    /api/agents
POST   /api/agents
GET    /api/agents/{id}
PATCH  /api/agents/{id}
DELETE /api/agents/{id}

GET    /api/runs
GET    /api/runs/{id}
POST   /api/runs/{id}/continue
POST   /api/runs/{id}/resume
POST   /api/runs/{id}/cancel
POST   /api/runs/{id}/verify
GET    /api/runs/{id}/collaboration_steps
GET    /api/runs/{id}/replay
GET    /api/runs/{id}/projection
GET    /api/runs/{id}/usage
GET    /api/runs/{id}/model_requests
GET    /api/runs/{id}/artifacts
GET    /api/runs/{id}/artifacts/{artifact_id}
GET    /api/runs/{id}/artifacts/{artifact_id}/search
GET    /api/runs/{id}/episode

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
POST   /api/rag/evaluations/run
```

## Conversations and Agent Profiles

`PATCH /api/conversations/{id}` accepts `{"title":"..."}`. Deleting a
Conversation removes its persisted Messages and associated execution state from
the selected Store.

### Structured Task State

`GET /api/conversations/{id}/task-state` returns the latest structured state or
an empty version-0 state before the first update. `PATCH` accepts an
`expected_version` and ordered typed operations; stale versions return `409`
with `code=task_state_version_conflict` and do not modify the state.

The revision collection endpoint returns the immutable timeline in ascending
version order. The version endpoint returns one historical Revision directly.
Each Revision contains the submitted patch, resulting state snapshot, previous
and current versions, source actor/Run metadata, and commit time. These
Conversation-scoped endpoints enforce the same Workspace scope as Messages and
Runs.

New Runtime Snapshot v8 Runs receive Structured Task State context. Agent
execution also receives the runtime-owned `update_task_state` Tool. The Tool
applies the same patch contract and derives source identity from the active Run
rather than model arguments. See
[Structured Durable Task State](../runtime/task-state.md) for supported operations and
Context/Replay semantics.

`POST /api/agents` creates a reusable execution profile. `PATCH
/api/agents/{id}` updates only supplied fields:

```json
{
  "name": "Incident Responder",
  "description": "Diagnoses production incidents from runbooks and runtime evidence.",
  "system_prompt": "Separate evidence from assumptions and return ordered recovery steps.",
  "tools": ["calculator", "get_current_time"],
  "memory_enabled": true,
  "retrieval_enabled": true
}
```

AgentFlow uses its native Turn Engine for every Agent. Tool names must be
installed even when they are currently disabled at the platform layer. Custom Agents can be
archived with `DELETE /api/agents/{id}`; built-in Agents cannot be archived.
Creating a Run freezes the effective profile and, for Multi mode, its candidate
profiles, so later edits do not change Resume or Replay semantics.

See [Configurable Agent Profiles](../runtime/agent-profiles.md) for mode behavior, Router
participation, Snapshot ownership, and current boundaries.

## Chat, Runs, and Verification

`POST /api/chat` accepts an optional `completion_contract`. This field is the only trigger that opts a new Run into verification; chat mode and `VERIFICATION_*` server settings do not enable it automatically. When present, the server freezes the effective contract before creating the Run and does not publish `run.completed` until fresh Evidence satisfies its policy.

Runs created without the field remain `verification_status=not_required`. A contracted Run carries the same frozen contract through continue/resume operations. The terminal SSE `done` payload includes both the Run `status` and `verification_status`. `POST /api/runs/{id}/verify` only retries an existing contracted Run against its latest persisted assistant output; it returns `409` for an ordinary Run. Replay responses include `verification_evidence` and `verification_artifacts`.

`verification_status` always describes runtime Verification for that Run. It
does not report Automated or Manual Test results.

`GET /api/runs/{id}/collaboration_steps` returns the persisted records for
orchestration Stages. Each `CollaborationStep.id` is the `stage_id` used by its
related Run Events.

## Usage and Replay

`GET /api/runs/{id}/projection` returns the canonical Run, Usage, and Verification read models at one `as_of_sequence` watermark, plus stable runtime-invariant failures. Here, "projection" means a deterministic, non-persisted view derived from durable events and authoritative records; it is not a second source of truth or a resume checkpoint. Replay includes the same projection and retains its top-level `summary` and `usage_ledger` compatibility fields.

`GET /api/runs/{id}/usage` returns the immutable budget, effective totals, open model reservations, and append-only usage entries. The same `usage_ledger` is included in Replay. A reservation and settlement share one `operation_id`; the settlement replaces its estimate when totals are calculated.

`GET /api/runs/{id}/model_requests` returns physical Model Request Envelopes,
Capture metadata, referenced Context Manifests, and the reconstructability
invariant status. Capture content is omitted unless `include_content=true` is
explicitly supplied. Each record includes a source diff comparing the
Envelope's selected token totals with selected/excluded Manifest totals.
Expired Capture content is never returned. Both forms enforce the Run's
Workspace namespace.

Replay is the detailed aggregate assembled from stored records for one Run. It
includes the Conversation's ordered `task_state_revisions`; Context Manifest
references identify which version was visible to each Model Call. Replay also
includes `tool_artifacts` metadata for oversized Tool results. Artifact content
is opt-in through bounded, Workspace-scoped read/search endpoints; list and
Replay never include full content. `offset`/`limit` control reads, while `q` and
`max_matches` control search. Expired content returns `410 Gone`.

Episode Report is a compact projection derived from Replay for review or
export; it does not create another execution history or accounting source.
Trace-derived Episode errors include optional `kind`, `category`, and
`retryable` fields from the common failure contract. Legacy events without
structured failure metadata remain valid and expose only `source` and
`message`. HTTP errors retain the compatible `error` field and add stable
`code`, `source`, `category`, `retryable`, optional `operation`, and
`request_id` fields. Dynamic `5xx` responses expose only standard HTTP text;
the original internal error remains in server diagnostics rather than the
client payload. Streaming chat error chunks use the same fields.

Verifier-specific settings use a common `verifiers[].config` object. Built-in types are `command`, `http`, `json_schema`, `text_constraints`, `citation`, and `answer_relevance`. The last type binds the user question to the candidate output and records embedding cosine-similarity evidence; it is not a factuality or groundedness check. See [Verification](../runtime/verification.md) for exact config shapes, scope, extension points, and policy semantics.

## Tool Governance

`GET /api/tools` returns installed Tool descriptors and their current enabled
state. The enable/disable endpoints update the Tool Manager configuration and
persist it at `TOOL_CONFIG_PATH`. This platform-level switch is separate from
the per-Agent `tools` allowlist.

Tool execution applies security scope authorization, timeout, result-size,
panic-recovery, tracing, and concurrency policy after both selection layers
admit the Tool. Agent allowlists cannot expand operator-granted Resource,
Network, or Credential scope. See [Tool Security Policy and Scope](../tools/tool-security-policy.md),
[Configurable Agent Profiles](../runtime/agent-profiles.md#two-tool-control-layers), and
[Tool Execution Policy](../runtime/execution-controls.md#7-tool-execution-policy).

## RAG Evaluation

`POST /api/rag/evaluations/run` executes retrieval cases through the same
embedding, dense/lexical recall, RRF, reranker, relevance gate, and security
pipeline used by search and Agent Runs.

The request accepts exactly one of:

- legacy top-level `cases`, which retain `expected_document_ids`,
  `expected_chunk_ids`, and `expected_chunk_contains` compatibility;
- a versioned `dataset` using `rag-golden-dataset-v1`.

The Golden Dataset v1 contract is available as a
[machine-readable JSON Schema](../schemas/rag-golden-dataset-v1.schema.json) and a
[canonical Dataset and corpus](../knowledge/rag-golden-dataset.md). A Dataset has
a stable `id`, explicit `version`, optional tags/description, and uniquely
identified cases. Every case declares `query`, `answerable`, optional tags and
forbidden sources. Answerable cases require at least one expected source;
unanswerable cases cannot define expected sources.
The evaluation endpoint rejects unknown JSON fields so misspelled safety fields
cannot be silently ignored.

An expected or forbidden source can match by `document_id`, `chunk_id`,
`source_uri`, and/or required `content_contains` fragments. Fields inside one
source are ANDed; multiple sources are alternatives. If any forbidden source
appears in returned Top-K, the case fails. An `answerable: false` case passes
only when retrieval returns no result.

`required_source_count` optionally requires multiple distinct expected-source
definitions to appear. It defaults to one for backward compatibility and cannot
exceed the number of `expected_sources`. The Case rank is the first rank at
which the required number of sources has been accumulated.

Optional `min_acceptable_rank`, Workspace/metadata scope, `top_k`, and
`min_similarity` make the evaluation repeatable. The response echoes Dataset
identity and reports aggregate Hit@1/3/5 and misses, plus each case's best rank,
failure reason, ranked items, and prompt-injection decisions. It also returns
the exact Embedding, Fusion, Reranker, and Relevance Gate identity used by the
run. `answerable_cases` and `unanswerable_cases` make the Hit@K denominator
explicit: no-answer cases contribute to pass/miss status but not Hit@K.
Dedicated no-answer Precision/Recall remains part of RAG-010.

The workbench exposes this endpoint under **Knowledge -> Retrieval evaluation**
and accepts either a Dataset object or a legacy case array. RAG-006 defines and
validates the schema; the maintained v1 asset and coverage matrix are described
in [RAG Golden Dataset v1](../knowledge/rag-golden-dataset.md). Immutable Dataset
storage/changelog (RAG-008), persisted Evaluation Runs (RAG-009), and calibrated
release thresholds remain future work.

## RAG Search Response

The example abbreviates hashes and omits optional fields. The live response also
includes `context_items` and `context_selection` when the relevance gate accepts
knowledge for model context.

```json
{
  "items": [
    {
      "document": {
        "id": "doc_123",
        "title": "Deployment guide",
        "version": "sha256:6f7a...",
        "content_hash": "6f7a..."
      },
      "chunk": {
        "id": "chunk_456",
        "document_id": "doc_123",
        "parent_id": "parent_3145f56f93c43b551a60df5d",
        "section_path": ["Operations", "Deploy"],
        "start_offset": 128,
        "end_offset": 384,
        "document_version": "sha256:6f7a...",
        "content_hash": "a51c...",
        "chunk_index": 2,
        "content": "Deploy the service after the smoke tests pass.",
        "token_count": 12,
        "metadata": {}
      },
      "similarity": 0.71,
      "recency_boost": 0.03,
      "score": 0.74,
      "vector_rank": 2,
      "lexical_rank": 1,
      "lexical_score": 0.92,
      "rrf_score": 0.0325,
      "fusion_rank": 1,
      "rerank_rank": 1,
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
  },
  "fusion": {
    "algorithm": "rrf",
    "version": "rrf-v1",
    "rank_constant": 60,
    "dense_weight": 1,
    "lexical_weight": 1
  },
  "reranker": {
    "algorithm": "heuristic",
    "version": "heuristic-reranker-v1",
    "config_version": "heuristic-default-v1"
  },
  "relevance_gate": {
    "policy": "heuristic",
    "version": "heuristic-relevance-gate-v1",
    "config_version": "heuristic-relevance-default-v1"
  },
  "security": {
    "policy_version": "rag-prompt-guard-v1",
    "untrusted_context": true,
    "checked_candidates": 4,
    "blocked_candidates": 1,
    "decisions": [
      {
        "document_id": "doc_123",
        "chunk_id": "chunk_456",
        "action": "blocked",
        "reasons": ["instruction_override"]
      }
    ]
  }
}
```

### Source Traceability

Document and chunk hashes are full lowercase SHA-256 hex strings; the example
abbreviates them for readability. Chunk offsets are a half-open UTF-8 byte
range into the normalized source document (`start_offset` inclusive,
`end_offset` exclusive). An ingest request may provide `version`; otherwise the
server derives it from the normalized document content hash. The same chunk
source details are included in Agent `retrieved_chunks` trace payloads.

### Recall, Fusion, and Reranking

`vector_rank` and `lexical_rank` identify which independent recall paths found
the chunk. Either field can be omitted when that path did not return the chunk.
`lexical_score` is produced by lexical recall, while `lexical_boost` is a
separate feature added by the shared reranker. A lexical-only item has
`similarity: 0`.

`rrf_score` is calculated as `1 / (60 + vector_rank) + 1 / (60 + lexical_rank)`;
an absent rank contributes zero. `fusion_rank` is the ordering immediately
after RRF, while `rerank_rank` is the final ordering after evidence, metadata,
lexical, and diversity signals. The adapter-level `score` remains available for
diagnostics but is not compared across dense and lexical recall paths.

The top-level `fusion` object reports the exact algorithm version, rank
constant, and source weights used for the response. The same metadata is
returned by RAG evaluation and recorded in Agent retrieval traces so UI and
offline checks can reproduce the score without copying server constants.

The top-level `reranker` object identifies the active implementation and its
immutable configuration through `algorithm`, `version`, and `config_version`.
Optional `provider` and `model` fields are reserved for model-backed rerankers.
Search, evaluation, and Agent retrieval traces expose the same metadata.

The independent `relevance_gate` object identifies the policy that filtered
reranked candidates. The Gate derives `confidence` and `filter_reason` from
trusted source data rather than accepting reranker-provided values. Invalid
stage output is returned as a pipeline error, not a `no_match` result. See
[Knowledge / RAG](../knowledge/knowledge-rag.md#retrieval-pipeline) for the stage contracts.

### Context Selection

RAG search accepts an optional positive `knowledge_context_max_tokens`. The response
keeps ranked child hits in `items` and returns the actual model context in
`context_items`. Context items add:

- `context_role`: `matched_child`, `parent`, or `adjacent`
- `matched_chunk_id`: the ranked child that caused the item to be selected
- `source_chunk_ids`: source chunks represented by a merged context item
- `matched_chunk_ids`: ranked child hits represented by a merged context item
- `merged_chunk_count`: number of source chunks combined into the item
- `source_id`: stable per-response source alias such as `S1`

The top-level `citation_sources` array is the trusted source catalog for
`context_items`. Its entries include `source_id`, document identity and title,
the transformed context chunk ID, represented source chunk IDs, section path,
document version, and byte offsets. Source aliases are assigned after context
transformation, so merged context items receive one alias.

The top-level `context_selection` object contains `version`, `max_tokens`,
`tokens_used`, `matched_children`, `parent_chunks`, `adjacent_chunks`, and
`scope_filtered`. Its nested `transformation` object contains `version`,
`input_chunks`, `output_chunks`, `duplicates_removed`, `adjacent_merges`, and
`document_groups`. HTTP search defaults to 16,000 context tokens. Agent runs use
the frozen per-Run knowledge limit instead.

Agent retrieval traces record ranked hits as `matched_chunks` and model context
as `retrieved_chunks`. `matched_chunk_count` and `chunk_count` report their
respective sizes, while `context_selection` records the token-limit decision.
Merged `retrieved_chunks` also carry source and matched chunk IDs so Replay can
show how a synthetic context item was assembled.

### Native RAG Citations

Assistant output may cite selected knowledge with markers such as `[S1]`. The
terminal chat event and persisted assistant Message return a structured
`citations` array containing only markers resolved against the sources selected
in the final Context Manifest. Unknown or budget-excluded markers are omitted
from `citations`, exposed as `invalid_citation_ids` in the terminal event, and
recorded as `invalid_source_ids` in the `citation.resolved` trace event. This
native RAG protocol is separate from the optional completion verifier named
`citation`, which checks external Markdown links.

### Security and No-Match Semantics

The top-level `security` object reports the prompt-injection policy version,
candidate counts, and filtering decisions for both direct recall and expanded
context candidates. Decisions contain source IDs,
action, and reason codes but never copy blocked chunk content. Candidates with
`action: "blocked"` are removed before RRF. Evaluation cases include the same
security object, and evaluation summaries expose the aggregate
`blocked_candidates` count. Agent retrieval traces record this object as
`knowledge_security`.

By default, lexical recall may add chunks that are absent from dense Top-K.
Passing a positive `min_similarity` keeps vector-threshold behavior and excludes
lexical-only chunks; lexical rank and score can still annotate matching dense
results.

`no_match=true` is a successful search decision: recalled candidates did not
pass the current relevance policy. Provider, embedding, and Store failures are
returned as execution errors instead of being disguised as no-match results.
