# Tool Result Artifact Governance

AgentFlow keeps large Tool results recoverable without placing their full
content in model Context. Governance is centralized in `tools.Executor`, so a
new Binding receives the same behavior without Tool-specific spill code.

## Execution Contract

1. A Tool Handler returns a JSON-compatible value.
2. Executor serializes it once and applies the Binding's per-call
   `MaxResultBytes` plus the process-level batch budget.
3. An oversized result is deterministically secret-redacted before storage.
4. File Store or Postgres persists immutable content and metadata. The model
   receives only a bounded preview, opaque Artifact ID, hash, size, media type,
   and retrieval hint.
5. `artifact_read` can recover at most 16 KiB per model Tool Call;
   `artifact_search` returns bounded previews and byte offsets. HTTP consumers
   may read at most 64 KiB per request.

The Artifact owns `run_id`, `stage_id`, `turn_id`, `tool_call_id`, Tool name,
definition revision, content hash, original/stored byte counts, media type,
redaction metadata, creation time, and expiry. Physical paths are never part of
the public contract.

## Limits

```env
TOOL_RESULT_MAX_BATCH_BYTES=8000
TOOL_ARTIFACT_MAX_BYTES=5242880
TOOL_ARTIFACT_PREVIEW_BYTES=1000
TOOL_ARTIFACT_RETENTION=168h
```

- The batch budget is applied in source order to raw results and Artifact
  previews. Once exhausted, later Artifacts retain their reference but receive
  an empty preview.
- The per-Artifact maximum is a storage safety boundary, not extra Context
  capacity. A result beyond it returns a typed `artifact_unavailable`
  degradation and never falls back to unbounded Context.
- Retention is persisted with metadata, so server restarts or removal of the
  original Tool do not change expiry. Expired content returns `410` over HTTP
  and emits `artifact.expired` when accessed by a runtime Tool.

## Persistence And Security

File Store writes content with exclusive create into an owner-only directory
and stores only Artifact metadata in the main JSON file. Postgres stores the
same metadata and immutable `bytea` content in `tool_artifacts`; Run deletion
cascades to both. Both adapters enforce Run ownership, content hash, byte size,
bounded reads, bounded search, and expiry.

Deterministic redaction covers credential-shaped keys and embedded Bearer,
OpenAI, and GitHub token patterns. Redaction failure occurs before persistence
and fails closed. Artifact persistence failure remains orthogonal to Tool
execution: the result is explicitly incomplete, carries
`artifact_unavailable`, and exposes only a bounded safe preview.

## Replay And Observability

Replay includes `tool_artifacts` metadata. Tool completion payloads include the
Artifact reference, and typed events record:

- `tool.result.persisted`
- `artifact.read`
- `artifact.expired`

Context Manifest entries for Tool result previews contain `artifact_ids`, which
links the exact model-visible input back to durable content. The HTTP recovery
surface is:

```text
GET /api/runs/{run_id}/artifacts
GET /api/runs/{run_id}/artifacts/{artifact_id}?offset=0&limit=8192
GET /api/runs/{run_id}/artifacts/{artifact_id}/search?q=needle&max_matches=5
```

All endpoints are Workspace-scoped through the owning Run.

## Current Scope

The first version supports JSON/text results. It does not implement image or
audio previews, object storage, background compaction, or legal-hold policy.
Access is blocked immediately from the persisted expiry time. Store startup
purges expired content bytes while preserving metadata for Replay and audit;
Run deletion removes the remaining metadata.
