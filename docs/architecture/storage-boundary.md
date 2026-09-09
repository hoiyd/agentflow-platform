# Storage Boundary

PostgreSQL is the application persistence backend. The File Store retirement
separates durable storage from the state fixtures used by tests and evaluations.

## Consumer Inventory

| Consumer | Destination |
| --- | --- |
| Server wiring and configuration | PostgreSQL only; no automatic fallback |
| Agent, HTTP, memory, context and tool behavior tests | In-memory fixture containing only consumed capabilities |
| RAG and Tool evaluation runners | In-memory fixture; preserve offline retrieval and report semantics |
| Restart, transaction, mutation and reconciliation persistence tests | PostgreSQL integration tests |
| File/Postgres contract parity tests | Keep PostgreSQL assertions, remove the File arm |
| JSON loading, saving, legacy normalization and file failure tests | Remove with the file persistence implementation |

The fixture is not a supported backend and must not be imported by the server.
It does not claim durability, multi-process safety or PostgreSQL query parity.
New production capabilities do not automatically require a fixture implementation.
Use existing narrow interfaces and small fault stubs where sufficient.

Pure domain validation shared by PostgreSQL and fixtures must not be duplicated.
Tests of restart durability must reconnect to PostgreSQL rather than reuse an
in-memory object. Database tests require an isolated test database, bounded
execution and cleanup; never point them at application data.

CI checks the transitive dependencies of `cmd/server` and rejects any
`internal/testsupport` import. RAG/Tool evaluation commands are the intentional
non-test consumers; their fixture implements state needed by existing runtime
interfaces, without load/save, a filesystem path, or restart semantics.

## Verification

Run from `apps/api` with Go configured as described in `AGENTS.md`:

```bash
# Offline behavioral tests; PostgreSQL tests explicitly skip without a URL.
go test ./... -timeout=3m
# Full acceptance: use a dedicated PostgreSQL server with pgvector and CREATEDB.
TEST_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5432/agentflow_test?sslmode=disable' \
  go test ./... -count=1 -timeout=3m -coverpkg=./... -coverprofile=coverage.out
go vet ./...
```

The migrated request-capture, mutation, reconciliation and Workspace suites
create randomly named disposable databases and drop them at cleanup. Existing
Postgres suites use the dedicated test database directly. CI supplies pgvector
and runs both groups; do not mistake a no-database test pass for durable acceptance.

The offline RAG baseline retains the same gate result and quality metrics;
fixture lexical scoring is intentionally not a substitute for PostgreSQL query
or transaction tests. API DTOs and frontend behavior are unchanged.

Existing user JSON files are not deleted. Importing historical data is outside
this change. Offline evaluation still means no model/network dependency; tests
that prove durable behavior explicitly require a local PostgreSQL service.
