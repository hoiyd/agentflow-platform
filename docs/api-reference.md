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

Runs created without the field remain `verification_status=not_required`. A contracted Run carries the same frozen contract through continue/resume operations. `POST /api/runs/{id}/verify` only retries an existing contracted Run against its latest persisted assistant output; it returns `409` for an ordinary Run. Replay responses include `verification_evidence` and `verification_artifacts`.

Verifier-specific settings use a common `verifiers[].config` object. Built-in types are `command`, `http`, `json_schema`, `text_constraints`, and `citation`. See [Completion Verification](completion-verification.md) for exact config shapes, scope, extension points, and policy semantics.

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
