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
    memory/         semantic memory operations and asynchronous curation
    knowledge/      knowledge-base ingestion, embedding, search, and RAG evaluation
    rag/            chunking and shared recall, reranking, and relevance gating
    concurrency/    run and model-request limits
    budget/         per-Run resource accounting and enforcement
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
    +--> internal/httpapi --> consumer-defined service interfaces
    |             |                    |
    |             |                    +--> memory, knowledge
    |             |
    |             +--> internal/agent --> internal/turn
    |                                      |
    |                                      +--> context, events, tools, model adapter
    |
    +--> store, recovery, concurrency, config
```

`cmd/server` is deliberately thin. It reads process configuration, creates the application, binds OS signals, and reports lifecycle errors.

`app` is the composition root. It creates every long-lived service and concrete adapter, applies runtime policies, injects a complete dependency set into the HTTP handler, owns the HTTP server, and closes background work before persistence.

`internal` is Go's module-private visibility boundary, not a lower architectural layer. Keeping product implementation under `internal` prevents other modules from accidentally depending on unstable backend packages. Packages inside it should continue to expose the smallest interfaces required by their consumers.

## Design Decisions

### Use a real composition root

Dependency construction belongs in `app`, not in `cmd/server` and not in HTTP handlers. Handlers do not construct runtimes, queues, or default policies and do not rely on post-construction setters. This keeps process bootstrapping testable and gives all executables one canonical wiring path.

### Do not mirror generic Clean Architecture folders

Empty `api`, `core`, and `services` directories do not create useful boundaries. AgentFlow groups code by concrete capability and changes package boundaries only when ownership or dependency pressure justifies it.

### Keep transport and execution separate

`httpapi` owns HTTP parsing, SSE encoding, and error-to-status mapping. Cross-resource operations live in named capabilities: `memory.SemanticMemory` owns validation, embedding, and memory persistence; `knowledge.KnowledgeBase` owns ingestion, query embedding, shared retrieval, and RAG evaluation. Agent execution remains in `agent` and `turn`.

The HTTP package defines the small service interfaces it consumes. Concrete implementations are selected only by `app`, so adding caching, another embedding implementation, or a different service adapter does not change transport code.

### Preserve private implementation packages

Moving packages out of `internal` only to make the tree appear balanced would weaken encapsulation without improving behavior. A package should become public only when another Go module has a supported need to import it.

## Application Lifecycle

`app.Application` owns:

1. Store selection and stale-run recovery.
2. Model client limits, retry policy, and frozen Run Budget policy.
3. Tool Manager and Agent Runtime configuration.
4. Memory and Knowledge capabilities plus asynchronous Memory Curation.
5. Run admission and Context Assembly policy.
6. HTTP server startup and graceful shutdown.
7. Memory Curator drain followed by Store close.

## Main Call Paths

```text
HTTP chat request
  -> run admission
  -> Agent Runtime
  -> Turn Engine
  -> typed Run Events
  -> shared Run completion
  -> message persistence and asynchronous memory curation

HTTP RAG search or Agent context retrieval
  -> Knowledge Base
  -> query embedding
  -> Retrieval Pipeline
       -> Store dense recall
       -> Store lexical recall
       -> deduplicate / rerank / relevance gate

HTTP document ingestion
  -> Knowledge Base
  -> chunking / embedding
  -> Store
```

The Retrieval Pipeline, rather than an Agent runtime or HTTP handler, owns the
recall sequence and result policy. Stores expose separate dense and lexical
search operations and remain responsible for applying workspace and metadata
filters. This keeps HTTP, Single-Agent, Multi-Agent, and Autonomous retrieval
behavior aligned while allowing File and Postgres adapters to use different
index implementations.

Single-Agent, Multi-Agent, and Autonomous streams expose the same `domain.RunEvent` contract to the HTTP adapter. Their common completion path persists the assistant message, transitions the Run, optionally generates the conversation title, flushes the final SSE event, and then schedules conservative curation of explicitly durable user facts. Assistant output and ordinary chat remain conversation history rather than long-term memory.

Adding another executable should reuse the relevant application wiring instead of copying dependency construction from `cmd/server`.
