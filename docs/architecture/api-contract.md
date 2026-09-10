# API Contract

AgentFlow uses [OpenAPI](../../api/openapi.yaml) as the source of truth for the
HTTP surface shared by the Go adapter and the TypeScript workbench. Business
handlers remain implemented with `net/http`; generation is limited to transport
DTOs and frontend path/schema types.

## Ownership

- `api/openapi.yaml` owns covered paths, request fields, response DTOs, enums,
  and the structured error envelope.
- `apps/api/internal/apicontract/types.gen.go` is generated and used at the Go
  request/response boundary.
- `apps/web/lib/api-contract.gen.ts` is generated and supplies DTOs to the
  existing frontend client.
- `apps/web/lib/api-client.ts` continues to own workspace headers, redaction-aware
  errors, response validation, and transport behavior. OpenAPI does not create a
  second fetch stack.

The initial contract covers health, conversations and messages, streaming Chat,
Agents, the primary Run lifecycle, Replay/Usage, collaboration steps, and Tools.
New endpoints should be added when they gain a frontend consumer or a stable
external contract.

## Change Workflow

1. Update `api/openapi.yaml` before changing a covered API shape.
2. Run `make contract-generate` from the repository root.
3. Update Go adapters and frontend consumers until both applications compile.
4. Run `make contract-check`; generated files are committed and must have no
   drift.

Generation is pinned to `oapi-codegen` v2.8.0 and `openapi-typescript` 7.13.0.
Generated files must not be edited manually.

## Frontend Component Tests

`npm test` runs both transport/unit tests and focused React component tests.
Component tests cover behavior that is easy to regress without requiring a
browser screenshot: API connection state, latest-request document selection,
and Replay degradation when an optional Episode Report is unavailable.
