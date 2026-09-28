# Scoped Knowledge Read Tools

## Contract and Test Plan

`knowledge_search` locates bounded, gated Knowledge chunks through the existing
KnowledgeBase/RAG pipeline. `knowledge_read` pages a chunk previously returned
by a successful search in the same Run. Neither tool accepts a Workspace or path.
The persisted Run supplies the Workspace namespace; this is not authenticated
Membership or document ACL enforcement.

## Availability and Call Contract

Production composition installs both bindings from the existing KnowledgeBase.
Like Artifact tools, they are harness capabilities added to new Run snapshots
when Tool Calling is available, not separate Tools-page switches. Frozen
operator policy still controls access; `retrieval_enabled` controls automatic
pre-Turn retrieval, not this explicit Tool capability. Old Run snapshots do not
silently acquire the new tools. Both bindings declare Workspace `knowledge`
read access and use the shared Executor, Tool budget, Progress Guard and tracing.
They remain serial; no new parallel scheduling or recovery protocol is added.

| Tool | Arguments | Bounded result |
| --- | --- | --- |
| `knowledge_search` | Required `query` (1-400 characters); optional `max_results` (1-5, default 3). | Reuses query embedding, hybrid ranking, security/relevance gates and index compatibility. Each locator has a stable Chunk reference, Document ID/version, confirmed index identity, content hash, title (192 UTF-8 bytes) and preview (384 bytes). Empty matches return `no_match` and a reason. |
| `knowledge_read` | Required `reference`; optional UTF-8 byte `offset` (default 0) and `limit` (1-4096, default 2048). | Returns a complete page, `next_offset`, `total_bytes`, `truncated` and trusted `[S#]` source metadata. Continue from `next_offset` only while `truncated=true`. |

Only successful, non-spilled searches recorded in the same Run issue readable
references. Reading performs a scoped Document lookup and checks version, Chunk
ownership, content hash and the index identity already validated by that search.
Search uses the Run's frozen embedding endpoint/model. The confirmed index
identity avoids confusing a routing label such as `local` with the embedding
transport label `openai_compatible`. Deletion, replacement or reindexing requires
searching again; an index mismatch remains the typed
`knowledge_index_incompatible` failure used by the HTTP 409 boundary.

This is namespace isolation, not user authentication. There are no path,
Workspace-switch, full-corpus scan, or Memory-write arguments.

## Context and Citation Evidence

Search previews are locators, not new citation evidence. Read excerpts remain
untrusted data. Only intact read observations selected into model Context may
contribute to the existing `[S#]` citation protocol, alongside automatic RAG.

One Run namespace reserves aliases for search locators so several reads in one
Tool batch cannot collide. Reservation is not evidence: only automatic sources
and actual selected, successful read pages can resolve citations. Search-only,
unknown, spilled and Context-compacted markers remain invalid.

The current Turn consumes paired Tool observations through the existing Tool
result limit. Later stages in Multi/Loop may select prior-stage pages through
the existing Knowledge token budget, wrapped as untrusted JSON data. The Manifest
uses the Tool Call ID as the page reference while citation metadata retains the
original Chunk ID. Excluded pages grant neither citations nor Tool authority.

`grounded_answer` checks only the delivered, selected read pages, not unread
bytes loaded from a current Document. If automatic RAG also selected the same
source, its original expanded evidence remains available rather than being
replaced by a smaller Tool page. Assistant Message citations persist optional
`run_id`, `tool_call_id` and `tool_event_id` in the existing JSON citation column;
no database table or column is added. Frontend Source details links Tool evidence
to the original Run event; older automatic citations remain compatible.

Results remain subject to the shared 16,000-byte per-binding limit and batch
Artifact governance. Artifact previews are not citation evidence. Read an
appropriately smaller Knowledge page to obtain citeable evidence; this feature
does not interpret arbitrary Artifact text as a new Knowledge source.

## Failure Inventory

| Failure | Expected result |
| --- | --- |
| Missing or conflicting Run/Conversation scope | Reject before embedding or document access. |
| No gated match or blocked prompt injection | Return an explicit empty search result; issue no readable reference. |
| Index incompatibility / embedding failure | Preserve typed failure; do not silently fall back to another index. |
| Guessed reference or reference from another Run | Reject without reading arbitrary documents. |
| Deleted, replaced or cross-Workspace document | Fail closed after scoped lookup and version/content-hash validation. |
| Invalid UTF-8 offset, oversized or zero page | Reject; successful pages always provide a usable next offset. |
| Malicious or credential-bearing current content | Apply the existing Knowledge security guard before returning text. |
| Tool result compacted or spilled to Artifact | Do not promote missing/incomplete text to citation evidence. |
| Existing automatic source and new Tool source | Stable Run source IDs do not collide; citation provenance includes the Tool event. |
| Multiple chunks read in one Tool batch | Distinct reservations prevent alias collisions before completion events are persisted. |
| Forged marker or source not selected into Context | Return an invalid citation, not trusted source metadata. |

Tests are backend integration tests with deterministic local model/embedding
fixtures and an in-memory Fixture Store, not browser-to-API E2E or a live-model
quality benchmark. Scope, retrieval decisions, Tool events, final Context and
persisted citations are checked together.

Run from `apps/api` with the repository Go version:

```bash
go test ./internal/httpapi -run '^TestScopedKnowledgeTools' -count=1 -v
go test ./internal/knowledge ./internal/rag ./internal/contextassembly -count=1
```

The first command emits `knowledge_tool_evidence` JSON for each execution mode:
input, Tool query, Workspace, Run/Chunk/Call/Event IDs, model-call count, outcome
and fixture limitations. Redirect its output to retain a repeatable evidence log.
These checks do not claim live-model citation quality or authenticated ACLs.
