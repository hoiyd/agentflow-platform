# Backend Architecture

AgentFlow uses a small Go application architecture centered on explicit package ownership and dependency direction. The directory depth is not intended to represent architectural importance.

## Package Layout

```text
apps/api/
  cmd/server/       process entry point: config, signals, exit reporting
  app/              application composition root and process lifecycle
  internal/
    domain/         persisted entities and shared contracts
    httpapi/        HTTP transport and route handlers
    agent/          run orchestration and execution modes
    turn/           shared Turn Engine
    event/          typed execution events and tracing
    contextassembly/
    contextcompaction/
    tools/          tool catalog and guarded execution
    openai/         model-provider adapter
    store/          File and Postgres persistence adapters
    memory/         asynchronous memory synchronization
    rag/            chunking and retrieval ranking
    concurrency/    run and model-request limits
    recovery/       stale-run recovery
    config/         environment configuration
```

## Dependency Direction

```text
cmd/server
    |
    v
app
    |
    +--> internal/httpapi --> internal/agent --> internal/turn
    |                              |
    |                              +--> context, events, tools, model adapter
    |
    +--> internal/store, recovery, concurrency, config
```

`cmd/server` is deliberately thin. It reads process configuration, creates the application, binds OS signals, and reports lifecycle errors.

`app` is the composition root. It creates concrete adapters, applies runtime policies, wires the HTTP handler, owns the HTTP server, and closes background work before persistence.

`internal` is Go's module-private visibility boundary, not a lower architectural layer. Keeping product implementation under `internal` prevents other modules from accidentally depending on unstable backend packages. Packages inside it should continue to expose the smallest interfaces required by their consumers.

## Design Decisions

### Use a real composition root

Dependency construction belongs in `app`, not in `cmd/server` and not in HTTP handlers. This keeps process bootstrapping testable and gives all executables one canonical wiring path.

### Do not mirror generic Clean Architecture folders

Empty `api`, `core`, and `services` directories do not create useful boundaries. AgentFlow groups code by concrete capability and changes package boundaries only when ownership or dependency pressure justifies it.

### Keep transport and execution separate

`httpapi` owns HTTP parsing and responses. Agent execution remains in `agent` and `turn`; model access, persistence, tools, and tracing remain separate adapters or capabilities.

### Preserve private implementation packages

Moving packages out of `internal` only to make the tree appear balanced would weaken encapsulation without improving behavior. A package should become public only when another Go module has a supported need to import it.

## Application Lifecycle

`app.Application` owns:

1. Store selection and stale-run recovery.
2. Model client limits and retry policy.
3. Tool Manager and Agent Runtime configuration.
4. Run admission and Context Assembly policy.
5. HTTP server startup and graceful shutdown.
6. Memory Syncer drain followed by Store close.

Adding another executable should reuse the relevant application wiring instead of copying dependency construction from `cmd/server`.
