# Runtime Knowledge Scope

Knowledge scope limits which Knowledge evidence a Run may use. It stays fixed across
retrieval, Tools and recovery; it does not freeze document contents.

## Workbench

- **Knowledge -> Search:** Product, Environment and Language apply Document-level
  equality filters. Blank fields are unrestricted; Clear removes all three.
- **Chat -> Knowledge:** Apply saves the draft; Cancel discards edits. Settings
  are locked during execution or input/approval waits.
- **Run replay -> Saved Knowledge scope:** inspect the saved filter, source,
  policy and hash. Unfiltered Runs have no panel.

Search and Chat share a Workspace-keyed, tab-local session-storage draft.
Changes invalidate pending search results and affect only new searches/Runs.
They are not Agent defaults and do not enable retrieval or grant Tools.

## Runtime Contract

- **Input:** `POST /api/chat` accepts optional `knowledge_filter` using the
  [typed syntax](typed-metadata-filters.md), including matching stored schema
  eligibility. Validate before creating Conversation/Message/Run records or
  calling a model. Limits: 2 MiB Chat JSON, 16 KiB filter. Reject explicit null,
  ambiguous JSON and invalid expressions; omit the field for no filter.
- **Snapshot:** `runtime_snapshot.knowledge_scope` stores the detached normalized
  filter, `source=run_request`, `policy_version=knowledge-filter-v1` and SHA-256
  identity. Exact decimals and UTC dates survive persistence. The hash identifies
  the representation, not logical equivalence, a signature or an access grant.
- **Execution:** Single, Multi-Agent and Bounded Loop apply the same predicate
  to automatic retrieval, parent/adjacent expansion and `knowledge_search`.
  Tools expose no filter/Workspace overrides, so the model cannot widen scope.
- **Source checks:** `knowledge_read` requires a same-Run search receipt and
  rechecks version, index identity, hash and eligibility on every page.
  Citation/grounding resolution checks every contributing Chunk, including
  merged evidence. Changed or out-of-scope sources fail explicitly; recorded
  evidence is not rewritten.
- **Recovery:** Continue, Resume and queued follow-ups inherit the originating
  Run's scope, not current browser/Agent settings. Invalid saved policy/hash
  returns `409 invalid_metadata_filter` (SSE error after streaming begins),
  without a model call, unfiltered fallback or loss of recoverable state.

## Compatibility and Limits

Scope uses existing Snapshot JSON; no migration is required. Old Snapshots
without it remain unfiltered. Run list/detail expose `knowledge_scope`; Replay
includes it in `runtime_snapshot`. Clients check `runtime_filtering` on
`GET /api/documents/metadata-schema` before filtered Chat. Do not use mixed-version
workers or Resume scoped Runs on older binaries.

Current Workspace membership/deletion checks still apply; archived Workspaces
allow reads, not new Runs. Scope is not Document ACL, expiry, automatic filter
planning, Memory/history filtering or proof of relevance. Do not store secrets
in metadata or filters; existing input redaction remains enforced.

## Verification

Use disposable Postgres via `TEST_DATABASE_URL`:
[gate setup](../operations/functional-regression-testing.md#run-and-evidence).

```bash
# apps/api
go test ./app -run '^TestProductionRuntimeKnowledgeFilter$' -count=1 -v
go test ./internal/knowledge -run '^TestKnowledgeToolsFrozenFilterAndStalePages$' -count=1 -v
# apps/web
npm run test:e2e -- runtime-knowledge-filters.spec.ts --repeat-each=2
```

Backend tests cover three modes, restart/Resume, corrupt scope, stale sources
and revoked access. Browser evidence retains `runtime-filter-evidence.json`
(synthetic inputs, identities, results and saved scope) and desktop screenshots.
Model/embedding transport is controlled: this proves contracts, not live quality.
