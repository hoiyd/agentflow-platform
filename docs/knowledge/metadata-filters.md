# Metadata Equality Filters

`DocumentSearch.metadata` is the existing `map[string]string` filter. Semantic
recall, lexical recall, parent/adjacent lookup, and the Context selector's scope
recheck use the same eligibility contract. PostgreSQL's existing JSONB `->>`
predicate is the compatibility baseline; this is not a new typed query language.
For typed operators, explicit layer addressing and schema eligibility, see
[Typed metadata filters](typed-metadata-filters.md). The contracts are separate;
when both are supplied their predicates are ANDed.

## Contract

For every requested key, **Chunk equals value OR Document equals value** must
hold. All requested keys are combined with **AND**, independently of which layer
matches. A conflicting Chunk value does not override a matching Document value.

| Input | Result |
| --- | --- |
| Omitted/empty filter | No metadata restriction; Workspace still applies |
| Document `env=prod`, Chunk `env=dev` | Either `env=prod` or `env=dev` matches |
| Keys supplied by different layers | Match only if every key is satisfied |
| Whitespace or case differs in key/value | No match; no trimming or case folding |
| Key missing or JSON null on both layers | Never matches, including `""` and `"null"` |
| JSON empty string | Matches `""` |
| Number `1` or string `"1"` | Both match filter text `"1"`; neither implies numeric comparison |
| Boolean `true` | Matches `"true"`, not `"True"` |
| Stored number `1.00` | Matches `"1.00"`, not `"1"`; exponent notation is rendered as decimal text |
| Array/object | Whole JSONB text equality, not membership or nested-field lookup; spacing/key order matter |

For example, a stored JSON array `["prod",true]` matches the filter value
`["prod", true]`, not `prod` or `["prod",true]`. Prefer scalar business labels;
literal array/object matching exists only to preserve the old SQL behavior.

Filtering occurs before recall limits. Expansion additionally requires the
matched Document and Workspace. Metadata is neither ACL nor a replacement for
current authorization, prompt-injection checks, or the relevance gate.

## Implementation and Boundaries

`rag/metadatafilter` supplies the shared in-memory predicate. PostgreSQL keeps
its parameterized predicates; metadata keys and values never become SQL code.
Document/Chunk reads preserve numbers as `json.Number` so an SQL-accepted value
does not lose precision or scale before the Context recheck. HTTP still emits
JSON numbers, not strings; this does not add arbitrary-precision JavaScript
numbers or preserve numeric formatting lost earlier by an ingestion client.

Workbench and Run/Tool filtering use [Runtime Knowledge scope](runtime-knowledge-filters.md),
not this legacy map. Memory filtering is unchanged.

## Verification

From `apps/api`, set `TEST_DATABASE_URL` to a dedicated test database with
`CREATEDB` privileges, then run:

```bash
go test ./internal/store -run '^TestDocumentMetadataFilter' -count=1 -json > /tmp/metadata-parity.jsonl
```

The tests create and drop disposable databases. JSON log entries retain synthetic
inputs, filters, Workspace/Document IDs, per-Store/per-path eligible IDs and
limitations. They compare eligibility, not ranking or live-model answer quality.
They also exercise actual Context selection and filtered recall beyond Top-K.
