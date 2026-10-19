# Metadata-only Updates

## Contract

`PATCH /api/documents/{id}/metadata` replaces custom Document metadata inside the
authorized Workspace. Send `expected_updated_at` from document detail and a
non-null `metadata` object. The existing microsecond-precision `updated_at` is
the concurrency token; no additional revision table is used.
The complete PATCH JSON body is limited to 64 KiB.

Custom fields removed from the object are removed from inherited Chunk fields.
A Chunk value matching the prior Document value is treated as inherited; a
different block-local value is retained. New fields fill absent Chunk values.
This preserves the existing Document-or-Chunk filtering contract; explicit field
ownership is a separate future schema concern.
Derived source/structure fields are preserved; changing them through this endpoint
is rejected. Changes to title, format or other indexing inputs require ingestion,
not a metadata patch. Custom labels do not enter embedding input today.

Document and Chunk metadata change atomically without changing IDs, content
hashes, versions, source ranges, embedding vectors or index identity. A successful
change advances `updated_at`; an identical payload retry returns the current
Document unchanged, even if its supplied nonzero token is stale. Other stale
updates return `409`. This no-op rule does not authorize overwriting newer values.
Preserve the returned timestamp string, including its microseconds; converting
it through a JavaScript `Date` loses precision. Document reads serialize UTC.
Retry/inheritance comparisons preserve JSON types and PostgreSQL numeric text
semantics: exponent normalization does not create a false conflict, while decimal
scale remains significant for the existing text-equality filter.

Same-source/body ingestion may supply `expected_updated_at` to use the same
metadata-only path. Without a token, changed labels return `409` instead of being
silently discarded. Existing content/index replacement remains a separate path.
The ingest shortcut rechecks indexing inputs under the same mutation lock; a
concurrent content/format/title replacement cannot masquerade as a label retry.
Metadata-only edits do not migrate an old embedding space; retrieval still checks
the stored index identity against the current route.

The optional `lifecycle` field uses the same conditional transaction; see
[Document lifecycle](document-lifecycle.md) for status, effective dates and current
source rechecks. Label updates alone do not establish validity, Document ACLs
or automatic enrichment.
For opt-in field types, schema adoption and declaration ownership, see
[Metadata schema](metadata-schema.md). Versioned updates validate effective
Chunk overrides inside the same transaction; legacy equality semantics remain unchanged.
The typed frontend client exposes `updateDocumentMetadata`. Knowledge detail
edits lifecycle; custom metadata tables remain read-only.

## Failure Inventory and Regression Evidence

| Failure | Required outcome |
| --- | --- |
| Missing token, null/non-object metadata, oversized payload or protected-field edit | Reject before mutation/embedding |
| Stale different payload | `409`, both Document and Chunks unchanged |
| Same payload replay | Same token, IDs and embedding count; no additional embedding calls |
| Two concurrent different edits | One commit and one conflict; no mixed Chunk metadata |
| Chunk write failure | Entire transaction rolls back, including Document token |
| Document write failure / unavailable Store | Internal failure, never reported as a successful or invalid-input update |
| Canceled update before admission | No Store mutation |
| Same-source/body labels change | Explicit conditional update, never silent loss |
| Reindex between lookup and mutation | Conflict, even when labels happen to match the newer Document |
| Format/title/index input changes | Not eligible for metadata-only shortcut |
| Foreign/missing Document | `404` inside the requested Workspace |
| Removed or changed inherited fields | Semantic/keyword recall and parent/adjacent lookup see only the new inherited values |
| Independent Chunk value with the same key | Preserve the local value; do not turn a Document edit into a Chunk override |
| Invalid numeric representation or normalized size overflow | `400`, no metadata mutation or embedding call |

Focused HTTP/Store and browser gates use synthetic documents, controlled local
embeddings and disposable Postgres. Their artifacts demonstrate contracts, not
live retrieval quality or expiry semantics.

```bash
# From apps/api; TEST_DATABASE_URL must name a disposable test database.
go test ./internal/httpapi -run '^TestDocumentMetadataUpdates$' -count=1
# From apps/web, using the same dedicated test server:
npm run test:e2e -- knowledge-metadata.spec.ts
```

The browser gate retains `knowledge-metadata-evidence.json` through the existing
CI artifact upload. It records synthetic inputs, Workspace, Document/Chunk
identities, tokens, retries/conflicts, reload results and filtered searches. It
uses a controlled embedding provider; no model credentials or live quality score
are required.
