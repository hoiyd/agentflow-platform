# Metadata Query Migration

Knowledge Search and Golden evaluation accept only
[`metadata_filter`](typed-metadata-filters.md). The retired query field `metadata`
is rejected with `400` before embedding, including `{}`, `null`, or requests that
also contain a valid typed filter. Offline Golden loading rejects it too.

Migrate each query explicitly: declare a schema, select Document or Chunk fields,
and use values of the declared types. To preserve an intentional either-layer
match, use an `any` group with both qualified fields; combine independent keys
with `all`. Arbitrary extension keys and JSONB text coercion have no typed
equivalent. Documents without the matching stored schema are excluded; adopt a
schema explicitly through the [metadata update API](metadata-updates.md).

There is no runtime translation or automatic schema adoption. Stored Document
and Chunk `metadata`, ingestion/update APIs and Memory filters are unchanged.
Historical reports retain their original evidence; their legacy `evidence.metadata`
field is read-only and cannot be resubmitted as a query constraint.
