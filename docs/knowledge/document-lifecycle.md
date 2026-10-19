# Document Lifecycle

## Contract

Document `lifecycle` is owner-managed retrieval eligibility, separate from
free-form metadata and its Chunk overrides:

| Field | Meaning |
| --- | --- |
| `status` | `active` (default), `draft`, or `withdrawn`; only active is eligible |
| `valid_from`, `valid_until` | UTC half-open interval `[from, until)`; missing bounds are unlimited |
| `published_at` | Business publication time; informational, not authorization or an expiry gate |

Dates require RFC3339 with timezone, microsecond precision and UTC years
0001-9999. Writes normalize to UTC; an end must follow its start. Null, empty
strings and unknown fields are rejected. Legacy rows default to active/unbounded;
old metadata named `status` or `valid_until` is never promoted into policy.

Set lifecycle during JSON ingestion, or edit **Knowledge -> Documents -> Details**.
Updates use the [metadata PATCH concurrency contract](metadata-updates.md): omit
`lifecycle` to preserve it, or send `{}` to reset active/unbounded. No embedding,
content-version or index change occurs. Content reindexing preserves lifecycle;
a concurrent lifecycle change conflicts rather than silently reactivating a source.

## Enforcement and History

Lifecycle is mandatory even without user filters. Dense/keyword recall applies
it before SQL `LIMIT`; expansion, every `knowledge_read` page, model continuation
and final citation/grounding resolution recheck it. Saved search previews are also
checked before reuse; they still do not count as citation evidence. User OR groups, model labels
and Chunk metadata cannot widen this constraint. Existing recency ranking is unchanged.

Continue/Resume checks recorded sources against current documents before changing
Run state. Unavailable, replaced or newly ineligible evidence returns
`knowledge_source_unavailable` (`409` at recovery admission, or an SSE error after
streaming starts), without a model call or loss of the recovery point. Start a
new Run to search current sources. A revocation detected after execution has
already resumed fails that attempt rather than leaving it running; earlier
model requests cannot be recalled. Historical events, citations and completed
Replay remain unchanged; this does not retract an already delivered answer.
Checks are boundary reads, not a database lock held throughout model execution.

Startup adds `documents.lifecycle` idempotently; it does not reindex or reinterpret
legacy labels. Do not run older retrieval workers alongside lifecycle-aware ones:
they do not enforce this gate. Lifecycle is not a Document ACL or proof of accuracy.

## Verification

Use a disposable `TEST_DATABASE_URL`; no live model is required:

```bash
# apps/api
go test ./internal/httpapi ./internal/store ./internal/knowledge ./internal/agent -run 'TestDocumentLifecycleHTTP|TestPostgresDocumentLifecycleMigrationAndRecall|TestKnowledgeReadRechecksLifecycle|TestRecoveryRejectsWithdrawnKnowledge' -count=1
go test ./app -run '^TestProductionRuntimeKnowledgeFilter$' -count=1
# apps/web
npm run test:e2e -- document-lifecycle.spec.ts
```

These gates cover UTC boundaries, inactive sources, pre-limit recall, stale writes,
reindex races, restart/Resume, real Tool reads and three runtime modes. Browser
artifacts retain synthetic inputs, revisions, results, limitations and a screenshot;
they prove eligibility contracts, not improved live answer quality.
