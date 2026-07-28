# API Summary

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
```

`POST /api/chat` accepts an optional `completion_contract`. This field is the only trigger that opts a new Run into verification; chat mode and `VERIFICATION_*` server settings do not enable it automatically. When present, the server freezes the effective contract before creating the Run and does not publish `run.completed` until fresh Evidence satisfies its policy.

Runs created without the field remain `verification_status=not_required`. A contracted Run carries the same frozen contract through continue/resume operations. The terminal SSE `done` payload includes both the Run `status` and `verification_status`. `POST /api/runs/{id}/verify` only retries an existing contracted Run against its latest persisted assistant output; it returns `409` for an ordinary Run. Replay responses include `verification_evidence` and `verification_artifacts`.

`GET /api/runs/{id}/usage` returns the immutable budget, effective totals, open model reservations, and append-only usage entries. The same `usage_ledger` is included in Replay. A reservation and settlement share one `operation_id`; the settlement replaces its estimate when totals are calculated.

Verifier-specific settings use a common `verifiers[].config` object. Built-in types are `command`, `http`, `json_schema`, `text_constraints`, and `citation`. See [Completion Verification](completion-verification.md) for exact config shapes, scope, extension points, and policy semantics.

Example RAG search response:

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

Document and chunk hashes are full lowercase SHA-256 hex strings; the example
abbreviates them for readability. Chunk offsets are a half-open UTF-8 byte
range into the normalized source document (`start_offset` inclusive,
`end_offset` exclusive). An ingest request may provide `version`; otherwise the
server derives it from the normalized document content hash. The same chunk
source details are included in Agent `retrieved_chunks` trace payloads.

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

The top-level `security` object reports the prompt-injection policy version,
candidate counts, and filtering decisions. Decisions contain source IDs,
action, and reason codes but never copy blocked chunk content. Candidates with
`action: "blocked"` are removed before RRF. Evaluation cases include the same
security object, and evaluation summaries expose the aggregate
`blocked_candidates` count. Agent retrieval traces record this object as
`knowledge_security`.

By default, lexical recall may add chunks that are absent from dense Top-K.
Passing a positive `min_similarity` keeps vector-threshold behavior and excludes
lexical-only chunks; lexical rank and score can still annotate matching dense
results.
