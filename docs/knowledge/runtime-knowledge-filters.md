# Runtime Knowledge Scope

## Workbench

- **Knowledge -> Search:** Product, Environment and Language restrict search
  using Document-level equality. Blank fields impose no restriction; Clear
  removes all three. Chunks still require the selected schema version.
- **Chat -> Knowledge:** edit the same Workspace-local draft, then Apply scope.
  Cancel discards edits. The toolbar shows the number of active constraints;
  settings are locked while a Run is active or waiting for input/approval.
- **Run replay -> Saved Knowledge scope:** expand to inspect the saved source,
  policy version, normalized filter and its SHA-256 identity. Unfiltered Runs
  add no panel. This is a filter record, not a copy of all Document metadata.

Drafts survive reload in the same browser tab via Workspace-keyed session
storage. They are not Agent defaults or server configuration. Changing a draft
invalidates pending search results and applies only to subsequent searches/new
Runs, never to an existing Run. These controls do not enable retrieval or grant
Tools to an Agent; its own configuration still determines those capabilities.

## Runtime Contract

`POST /api/chat` accepts optional `knowledge_filter`, with the same
[typed filter syntax and limits](typed-metadata-filters.md) as search. Validation
precedes Conversation/Message/Run creation and model calls. Chat JSON is limited
to 2 MiB; the nested filter remains limited to 16 KiB. Explicit null, ambiguous
JSON and invalid expressions are rejected, not treated as a clear operation.
Omit the field for an unfiltered new Run.

The application freezes a detached normalized filter into
`runtime_snapshot.knowledge_scope` with `source=run_request`,
`policy_version=knowledge-filter-v1` and a hash of the normalized filter JSON.
Numbers retain exact decimals; dates normalize to UTC. The hash identifies
this representation, not every logically equivalent permutation of an AND/OR.
It is not a signature or authorization token.

Single, Multi-Agent and Bounded Loop share this scope for automatic retrieval
and `knowledge_search`; parent/adjacent expansion uses the same predicate.
Knowledge Tools expose query/reference arguments, **not** filter or Workspace
overrides. A model cannot broaden the constraint. `knowledge_read` requires a
successful search receipt from the same Run and checks current source version,
index identity, content hash and eligibility on every page. Citation/grounding
resolution also rechecks the current labels for every contributing Chunk,
including immutable Tool pages and merged automatic evidence. Changed or
out-of-scope sources cause an explicit failure; old recorded evidence is not
silently rewritten.

Continue, Resume and queued follow-ups use the originating Run's saved scope,
not the current browser draft or changed Agent settings. Unsupported/corrupt
saved policy/hash cannot fall back to unfiltered retrieval; recovery reports
`409 invalid_metadata_filter` (an SSE error if streaming already began) without
spending a model request or retiring the recovery state. Current Workspace
membership/deletion checks remain authoritative. Archived Workspaces retain
their read-only search behavior, but cannot start new Runs.

The scope is stored in the existing Snapshot JSON; no new table/column or data
migration is needed. Old Snapshots without the optional field remain unfiltered.
Run list/detail responses expose `knowledge_scope` without revealing the whole
Snapshot; Replay returns it inside `runtime_snapshot`. New clients check
`GET /api/documents/metadata-schema.runtime_filtering` before submitting a
filtered Chat to prevent older servers silently ignoring the field. Avoid
mixed-version workers or resuming scoped Runs on older binaries that lack
this enforcement.

Scope is a business constraint, not Document ACL, expiry, automatic filter
planning, memory/history filtering or proof of relevance. The UI offers three
equality fields; the API supports the full bounded typed expression. Credential
content is rejected by the existing input redaction boundary; do not put secrets
in owner-declared metadata or filters.

## Failure Inventory

| Failure | Required outcome |
| --- | --- |
| Invalid or ambiguous Run filter | Reject before creating a Conversation/Message/Run or calling a provider |
| UI/Agent configuration changes after admission | Resume/Continue uses the immutable saved scope |
| Single, Multi or Loop drops the constraint | Same eligible IDs for automatic recall and Knowledge Tools |
| Model asks a broader query or guesses a reference | Query cannot widen scope; reads require an issued reference and current eligibility |
| Labels change after search or between pages | Refuse stale out-of-scope reads and citation resolution |
| Parent/adjacent expansion or citation reread widens scope | Reuse the same predicate, including every underlying Chunk of merged evidence |
| Missing/corrupt frozen policy or hash | Fail closed, never retry as an unfiltered request |
| Workspace access is revoked | Current authorization remains authoritative; a Snapshot never grants access |
| Filter changes during a search or Workspace switch | Discard obsolete results; persist drafts only in the owning Workspace |
| No filter or explicit clear | Keep existing unfiltered behavior; old Runs retain their own scope |

## Verification

Use a dedicated disposable Postgres via `TEST_DATABASE_URL`, never application
data. Follow the [functional gate setup](../operations/functional-regression-testing.md).

```bash
# apps/api: production commands, Runtime, real Bindings and PostgreSQL;
# three modes, restart/Resume, exact-number round-trip, corrupt identity,
# stale pages/citations and Workspace revocation.
go test ./app -run '^TestProductionRuntimeKnowledgeFilter$' -count=1 -v
go test ./internal/knowledge -run '^TestKnowledgeToolsFrozenFilterAndStalePages$' -count=1 -v
# apps/web: UI -> production Go -> disposable Postgres, twice.
npm run test:e2e -- runtime-knowledge-filters.spec.ts metadata-schema.spec.ts typed-metadata-filters.spec.ts super-local.spec.ts --repeat-each=2
```

Browser artifacts retain `runtime-filter-evidence.json` (synthetic corpus,
Workspace/Run identities, search responses and saved/reloaded/follow-up scope)
and desktop screenshots for search, Chat settings and Replay. The backend log
records mode, scope identity, Tool/retrieval paths and recovery outcomes. Only
model/embedding transport is controlled; this proves propagation and failure
boundaries, not live-model answer quality or statistical retrieval improvement.
