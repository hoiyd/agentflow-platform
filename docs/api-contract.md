# API Contract

AgentFlow uses [OpenAPI](../api/openapi.yaml) as the shared HTTP interface
definition for the Go API boundary and the TypeScript frontend client. The Go
business handlers remain implemented with `net/http`; code generation is limited
to transport DTOs and client code.

## Ownership

- `api/openapi.yaml` is the authoritative contract for covered endpoints,
  request and response fields, status values, and error responses.
- `apps/api/internal/apicontract/types.gen.go` contains generated Go DTOs.
- `apps/web/lib/api/generated.ts` contains generated TypeScript paths and DTOs.
- Generated files are committed for reproducible builds and must not be edited
  manually.

The initial contract covers Chat streaming, Agents, Runs, Run replay and usage,
Collaboration Steps, and Tools. SSE transport remains hand-written, while its
request and event DTOs come from the same contract.

## Development Workflow

After changing the API surface:

1. Update `api/openapi.yaml` first.
2. Run `make contract-generate` from the repository root.
3. Update Go adapters or frontend consumers until both projects compile.
4. Run `make contract-check` before committing.

`make contract-check` regenerates both outputs and fails when committed generated
files differ. Backend contract tests also validate representative domain response
payloads against the OpenAPI schemas.

The generators are pinned in the isolated `apps/api/tools/go.mod` module and
`apps/web/package-lock.json`. Use Go 1.25.5 and the repository's configured
Node.js/npm environment. The separate tools module prevents generator
dependencies from changing the backend runtime dependency graph.

## Adding an Endpoint

Define its path, operation ID, request, success response, and every expected error
status in OpenAPI. Reuse named component schemas instead of embedding anonymous
objects when a value is shared across endpoints. Keep persistence and runtime
models in `internal/domain`; convert generated transport DTOs at the `httpapi`
boundary when their representations differ.
