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

Existing user JSON files are not deleted. Importing historical data is outside
this change. Offline evaluation still means no model/network dependency; tests
that prove durable behavior explicitly require a local PostgreSQL service.
