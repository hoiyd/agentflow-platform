# HTTP API Reference

The API exposes persisted conversations, Agent execution, operational replay,
tools, semantic memory, and RAG knowledge. Streaming chat uses Server-Sent
Events (SSE); resource and inspection endpoints use JSON.

The current API does not implement authentication or authorization. It is
intended for local development or deployment behind a trusted access boundary;
the default `BIND_ADDRESS=127.0.0.1` keeps it local. `ALLOWED_ORIGINS` is a
browser CORS policy, not an authentication mechanism.

For lifecycle terminology, read [Internal terms](terms.md). For configuration
and security boundaries, read [Backend configuration](backend-configuration.md).

## Endpoint Map

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
POST   /api/runs/{id}/verify
GET    /api/runs/{id}/collaboration_steps
GET    /api/runs/{id}/replay
GET    /api/runs/{id}/usage
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

## Chat, Runs, and Verification

`POST /api/chat` accepts an optional `completion_contract`. This field is the only trigger that opts a new Run into verification; chat mode and `VERIFICATION_*` server settings do not enable it automatically. When present, the server freezes the effective contract before creating the Run and does not publish `run.completed` until fresh Evidence satisfies its policy.

Runs created without the field remain `verification_status=not_required`. A contracted Run carries the same frozen contract through continue/resume operations. The terminal SSE `done` payload includes both the Run `status` and `verification_status`. `POST /api/runs/{id}/verify` only retries an existing contracted Run against its latest persisted assistant output; it returns `409` for an ordinary Run. Replay responses include `verification_evidence` and `verification_artifacts`.

## Usage and Replay

`GET /api/runs/{id}/usage` returns the immutable budget, effective totals, open model reservations, and append-only usage entries. The same `usage_ledger` is included in Replay. A reservation and settlement share one `operation_id`; the settlement replaces its estimate when totals are calculated.

Verifier-specific settings use a common `verifiers[].config` object. Built-in types are `command`, `http`, `json_schema`, `text_constraints`, and `citation`. See [Completion Verification](completion-verification.md) for exact config shapes, scope, extension points, and policy semantics.

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

The independent `relevance_gate` object identifies the policy that classified
and filtered reranked candidates. Reranker-provided confidence is never trusted;
the Gate owns `confidence` and `filter_reason`. Invalid Reranker output, including
missing identity metadata, unknown candidates, invalid ranks, non-finite scores,
or non-normalized model-backed scores, is returned as a pipeline error.

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
recorded by a `citation.resolved` trace event. This native RAG protocol is
separate from the optional completion verifier named `citation`, which checks
external Markdown links.

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
