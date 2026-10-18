# Metadata Schema and Field Ownership

## Contract

`GET /api/documents/metadata-schema` describes `knowledge-metadata-v1`: field
types, declaration owner, Document/Chunk scope and bounds. JSON ingest and
multipart upload accept `metadata_schema_version`; PATCH can explicitly adopt
the version using the [existing concurrency token](metadata-updates.md).
Omitting the version on an existing source or PATCH retains its stored version.
Unknown versions fail with `400 invalid_metadata_schema`, never a legacy fallback.

For example, send a JSON ingest request with an explicit contract:

```json
{"title":"Release guide","content":"Release procedure...",
 "metadata_schema_version":"knowledge-metadata-v1",
 "metadata":{"product":"AgentFlow","environment":"production","tags":["release"]}}
```

| Owner-declared field | Type and limits |
| --- | --- |
| `product` | Nonblank string, up to 120 Unicode characters |
| `environment` | `development`, `test`, `staging`, `production` |
| `language` | Nonblank string, up to 35 characters; not a language detector |
| `tags` | Up to 32 nonblank strings, each up to 64 characters; order retained |
| `priority` | JSON number exactly in `[0,100]`; no string coercion or float64 rounding |
| `is_reference` | JSON boolean; a declaration, not verified authority |
| `published_at` | RFC3339 with explicit timezone and microsecond precision; stored as UTC |

All fields are optional. Missing fields remain absent; known fields reject
`null`. Unknown fields, including nested JSON and null values, are retained as
**unverified extensions** and are not registered for future typed filtering.
Each effective versioned Document/Chunk map permits 64 fields and 64 KiB, with
nonempty keys up to 64 bytes. Limits include derived fields, before embeddings.
Writes reject duplicate JSON names, multiple JSON values and depth over 32.
Request-struct names also reject case aliases; free-form metadata remains
case-sensitive. JSON ingestion is bounded to 2 MiB; PATCH remains 64 KiB.

## Ownership and Inheritance

- **System-derived:** entity/Workspace identity, hashes/index identity and
  source/Chunk structure cannot be owner labels. Versioned ingestion accepts
  `format`/`filename` only when consistent with explicit source inputs. PATCH
  may repeat unchanged derived values but cannot replace them.
- **Owner-declared:** business fields are validated declarations, not verified
  facts. Document fields inherit into Chunks; a different local value is an
  explicit Chunk override and must pass the same type contract. Qualified
  addressing is `document.metadata.<key>` versus `chunk.metadata.<key>`; neither
  gets a new typed-query syntax in this change.
- **Unverified extension:** neither a model-generated attribute nor an
  `access_scope`/"reviewed" label grants authorization, freshness or trust.
  There is no automatic model enrichment or per-field author/audit registry.

The Knowledge detail view shows the stored schema and separate expandable
Document/Chunk metadata tables with declaration ownership. It is read-only;
legacy records are labeled **Legacy / unvalidated**, even for familiar field names.
Existing [equality filters](metadata-filters.md) retain their cross-layer OR
semantics. Typed operators and hard expiry/ACL checks are separate features.

## Persistence and Compatibility

`documents.metadata_schema_version` is the single stored version. Chunks use
their owning Document's version; no duplicate version column is needed. Startup
migration adds the column with an empty default and never reclassifies or drops
legacy JSON. Reads do not revalidate old values, so incompatible legacy labels
remain inspectable and repairable after restart.
Historical custom fields using reserved system names cannot be certified by
adoption; reingest a clean source to repair them. Invalid independent Chunk
overrides likewise block adoption instead of being silently overwritten.

New clients opt in explicitly. Adoption replaces metadata conditionally, checks
effective Chunk overrides and atomically persists the version, inherited values
and `updated_at`, without embeddings. Even adoption with unchanged labels advances
the token. Ingest/Store replacement cannot silently downgrade a versioned source.
Retrieval and context expansion return the same stored Document version.

No operator migration command or production-data rewrite is required. An older
binary can ignore the additive column, but its writes do not enforce this contract;
avoid mixed-version writers and stop writers before rolling back.

## Failure Inventory

| Failure | Required outcome |
| --- | --- |
| Unknown schema version or wrong known-field type | Reject before embedding or mutation; never retry as legacy |
| Missing optional field / unknown extension | Preserve omission / extension JSON without inventing a default or trusted attribution |
| Null known field, invalid enum/date or oversized value | Validation error; no partial write |
| Duplicate JSON keys, including nested extension keys | Reject ambiguous input before decoding its business meaning |
| Caller supplies entity, scope or derived Chunk fields | Reject forged values; derive structure from the source |
| Legacy document has an incompatible custom value | Keep it readable as legacy; explicit adoption requires a valid replacement |
| PATCH omits schema on a versioned document | Retain and enforce the stored schema |
| Ingest or concurrent replacement would drop an existing schema | Reject, never silently downgrade |
| Schema adoption/update fails on a Chunk | Roll back schema, metadata and concurrency token together |
| Distinct typed Chunk override | Validate and preserve it; do not confuse it with inherited metadata |
| Database created before the schema column | Idempotent migration preserves all existing metadata and leaves legacy records unversioned |
| Reload, retrieval or expansion loses the schema | Return the persisted version consistently with Document metadata |

## Verification

Use a disposable Postgres with `TEST_DATABASE_URL`; never use application data.

```bash
# apps/api: HTTP -> Knowledge -> real fixture/Postgres; reopen and old-column migration.
go test ./internal/httpapi ./internal/store \
  -run '^(TestMetadataSchemaContract|TestDocumentMetadataUpdates|TestPostgresMigrationsAddMetadataSchema)$'
# apps/web: keep the CI-order Workspace regression alongside the affected feature.
npm run test:e2e -- knowledge-metadata.spec.ts metadata-schema.spec.ts super-local.spec.ts
```

Browser artifacts retain `metadata-schema-evidence.json` and a synthetic desktop
screenshot. The gate exercises real HTTP and Postgres with controlled embeddings;
it proves contract behavior and reload, not retrieval quality or document ACLs.
