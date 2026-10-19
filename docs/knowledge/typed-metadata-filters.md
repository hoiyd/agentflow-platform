# Typed Metadata Filters

## Request Contract

`POST /api/rag/search` accepts optional `metadata_filter`, independently of the
[legacy equality map](metadata-filters.md). When both are present they are
**ANDed**. The authorized Workspace and the required stored schema version wrap
the complete expression; a user `any` cannot widen either constraint.

```json
{
  "query": "production rollback procedure",
  "metadata_filter": {
    "schema_version": "knowledge-metadata-v1",
    "where": {
      "all": [
        {"field": "document.metadata.product", "op": "eq", "value": "AgentFlow"},
        {"field": "chunk.metadata.environment", "op": "eq", "value": "production"},
        {"field": "document.metadata.published_at", "op": "gte", "value": "2026-01-01T00:00:00Z"}
      ]
    }
  }
}
```

Every condition is exactly one nonempty `all` (AND) / `any` (OR) group, or one
`field`/`op`/`value` leaf. Limits: 4 levels, 32 nodes including groups, 32 members
per `in`, 16 KiB filter JSON and 64 KiB complete search JSON. Unknown properties,
duplicate keys, null filters/groups and mixed leaf/group nodes are rejected.
Invalid filters return `400`; validation happens **before query embedding**.

Only the seven owner-declared fields in the selected
[metadata schema](metadata-schema.md) are filterable. Address them explicitly
as `document.metadata.<key>` or `chunk.metadata.<key>`. Chunk addresses use the
stored effective Chunk metadata, including inherited labels and independent
overrides. A different Document value does not rescue a failing Chunk condition.

| Field type | Operators | Meaning |
| --- | --- | --- |
| string / enum / bool | `eq`, `in` | Typed equality / equality with any supplied member |
| number | `eq`, `in`, `gt`, `gte`, `lt`, `lte` | Exact decimal comparison, not string or float64 coercion |
| date-time | `eq`, `in`, `gt`, `gte`, `lt`, `lte` | Instant comparison; RFC3339, explicit timezone, microsecond precision; UTC years 0001-9999 |
| string-array (`tags`) | `contains`, `in` | One exact string member / overlap with any supplied string |

Field bounds and enum values come from the metadata schema. Case and whitespace
are significant. Missing/null fields never match, including within OR. Empty
`in` is invalid; an empty stored tag array matches nothing. Explicit `false`
and zero are values, not omission. No NOT, arbitrary JSONPath, nested extension
lookup or implicit "either metadata layer" operator is provided.

## Eligibility and Compatibility

Typed queries admit only Documents whose persisted version equals the supplied
schema version. Legacy/unknown-version records are excluded, not coerced or
automatically adopted. Without `metadata_filter`, the existing behavior and
legacy map contract are unchanged. Unknown extension labels such as
`access_scope` cannot be used as typed authorization facts.

`rag/eligibility` validates a detached predicate and supplies equivalent Go and
parameterized SQL evaluation. Semantic and keyword Stores apply it before
ranking/limits. Parent/adjacent lookup receives the same predicate; the Context
selector independently rechecks the returned rows. A recall adapter returning
an ineligible row causes `500 knowledge_eligibility_violation`, with no partial
sources/citations, rather than concealing post-limit filtering or scope escape.

Postgres uses the existing Workspace, JSONB/GIN, lexical and vector indexes;
this feature adds no schema migration or blanket expression indexes. The current
semantic query orders exact distance **plus recency**, not a raw ANN distance
Top-K. This preserves eligibility before the limit. `CASE` guards typed casts;
safe date parsing uses PostgreSQL 16's `pg_input_is_valid` (the deployment/test
baseline). Do not replace this query with prelimited ANN results and post-filter
them: [pgvector documents the recall trade-off](https://github.com/pgvector/pgvector#filtering).

The query-plan regression captures actual Store SQL with a test-only pgx tracer,
then runs `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` against 500 synthetic Chunks.
It records unfiltered, 50%, 1% and no-match counts, three individual elapsed
samples, server/extension versions, inputs and plans. It checks that no HNSW path
truncates selective eligibility. This small corpus does **not** establish a
production latency percentile or justify new indexes; measure a representative
corpus before changing plans or adding bounded ANN/iterative scans.

For Workbench controls, Run/Tool propagation and execution boundaries, see
[Runtime Knowledge scope](runtime-knowledge-filters.md).

## Failure Inventory

| Failure | Required outcome |
| --- | --- |
| Unknown field/schema/operator, wrong type, null or empty set | Reject before embedding, never fall back to an unfiltered search |
| Oversized/deep expression, ambiguous JSON or unknown properties | Bounded validation error, no provider call |
| Legacy or missing metadata | No typed match; legacy map semantics remain unchanged |
| Conflicting Document/Chunk values | Evaluate only the explicitly addressed layer |
| User OR or SQL-like text | Cannot widen Workspace, schema or legacy constraints; values stay parameters |
| Eligible rows below the global recall limit | Filter before ranking/limits, in both recall paths |
| Parent/neighbor has different labels | Exclude during lookup and selector recheck |
| A recall implementation returns an ineligible row | Shared pipeline fails closed before ranking/context/citations |
| Selective filter or no eligible row | Retain actual counts, plans and latency, never invent a match |

## Verification

Use only a disposable test Postgres with `TEST_DATABASE_URL`:

```bash
# apps/api: actual HTTP/Knowledge and Postgres/fixture predicates, expansion,
# pre-limit recall, adapter violations and actual production query plans.
go test ./internal/httpapi ./internal/store \
  -run '^(TestTypedMetadataFilters|TestTypedMetadataFilterStoreParity|TestTypedMetadataFilterBeforeRecallLimit|TestPostgresTypedFilterPlans)$' \
  -count=1 -v
# apps/web: affected browser -> production Go composition -> Postgres gate.
npm run test:e2e -- metadata-schema.spec.ts typed-metadata-filters.spec.ts super-local.spec.ts --repeat-each=2
```

The browser retains `typed-metadata-filter-evidence.json` with synthetic corpus,
typed inputs, persisted identities, responses, failure/reload results and embedding
counters. It exercises the API from a browser, not a filter-editor UI. Embeddings
are controlled; this is eligibility evidence, not live answer-quality calibration.
