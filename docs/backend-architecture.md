# Backend Architecture

AgentFlow uses a capability-oriented Go architecture centered on explicit
ownership, narrow consumer interfaces, and one application composition root.
Directory depth does not represent architectural importance.

For the reasoning behind the main boundaries, read
[Engineering decisions](engineering-decisions.md). For domain terminology such
as Run, Stage, Turn, and Model Call, read [Internal terms](terms.md). For the
complete Single, Multi, and Loop lifecycles, read
[Execution modes](execution-modes.md).

## Architectural Invariants

- `cmd/server` performs process startup; it does not construct product logic.
- `app` is the only production composition root.
- HTTP handlers translate transport concerns and depend on capability
  interfaces; they do not recreate Memory, Knowledge, or Runtime services.
- Single-Agent, Multi-Agent, and Autonomous execution share one Turn-level
  implementation for retrieval, tools, context, events, and model access.
- Stores persist domain contracts but do not own retrieval policy, reranking,
  or orchestration.
- Every `/api/*` operation resolves a non-empty Workspace namespace. Store lookups,
  retrieval expansion, Replay, and Verification preserve it across adapters.
- Run Events describe execution; the Usage Ledger remains the accounting
  authority.

## Performance, Concurrency, Tracing, and Verification

These are cross-cutting runtime capabilities, not mode-specific features:

| Capability | Runtime design | Adaptation boundary |
| --- | --- | --- |
| **Performance** | SSE streams output incrementally; context, output, Tool results, Run usage, and background queues are bounded. Active runtime excludes queue and human-wait time. | Limits and provider policies are configured at composition time and frozen into new Runs where replay stability requires it. No benchmark result is implied. |
| **Concurrency** | `RunController` combines global admission, bounded queueing, and per-Conversation single-writer execution. The model limiter separately owns in-flight requests and RPM/TPM; Tool bindings declare serial, read-only, or keyed parallelism. | Single, Multi-Agent, and Loop (`autonomous`) modes use the same controls. Provider retries acquire fresh permits without double-counting logical Run usage. |
| **Workspace scope** | HTTP resolves `X-Workspace-ID`, query, or payload scope to one normalized namespace; omitted and legacy `default` values become `default_workspace`. Persisted Runs inherit Conversation scope. | File and Postgres stores enforce namespace predicates. Authentication, Membership, and ACL remain separate future policy layers. |
| **Tracing** | Typed Run Events cover Run, Stage, Turn, Model, Tool, Retrieval, Context, Memory, Usage, and Verification lifecycles. `ModelRequestEnvelope` records each physical provider attempt against its Runtime Snapshot and Context Manifest. Durable records are persisted for Replay and debug views; streaming `model.delta` events are intentionally omitted. | Event contracts remain stable across File/Postgres stores and native/framework executors; adapters add versioned metadata instead of inventing private trace formats. |
| **Verification** | An opt-in frozen Completion Contract evaluates the persisted candidate output, records immutable Evidence/Artifacts, and gates `run.completed`. | Versioned verifier implementations are registered behind a narrow interface. Verification applies identically to all execution modes and is independent of Automated and Manual Tests. |

Performance controls prevent unbounded work and expose tuning surfaces; tracing
explains observed execution; Verification judges configured outcome invariants.
Keeping these responsibilities separate avoids using telemetry as enforcement
or treating successful execution as proof of a valid result.

See [Observability records and views](terms.md#observability-records-and-views)
for the distinction between Run Events, Trace, Replay, and Episode Report.

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
    failure/        shared failure classification and event projection
    contextassembly/
    contextcompaction/
    modelrequest/    request limiting plus provider-neutral observation contract
    requestcapture/  request envelope, capture policy, and reconstructability invariant
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

### Use a Real Composition Root

Dependency construction belongs in `app`, not in `cmd/server` and not in HTTP handlers. Handlers do not construct runtimes, queues, or default policies and do not rely on post-construction setters. This keeps process bootstrapping testable and gives all executables one canonical wiring path.

### Do Not Mirror Generic Clean Architecture Folders

Empty `api`, `core`, and `services` directories do not create useful boundaries. AgentFlow groups code by concrete capability and changes package boundaries only when ownership or dependency pressure justifies it.

### Keep Transport and Execution Separate

`httpapi` owns HTTP parsing, SSE encoding, and error-to-status mapping. Cross-resource operations live in named capabilities: `memory.SemanticMemory` owns validation, embedding, and memory persistence; `knowledge.KnowledgeBase` owns ingestion, query embedding, shared retrieval, and RAG evaluation. Agent execution remains in `agent` and `turn`.

The HTTP package defines the small service interfaces it consumes. Concrete implementations are selected only by `app`, so adding caching, another embedding implementation, or a different service adapter does not change transport code.

### Preserve Private Implementation Packages

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
  -> Workspace scope resolution
  -> run admission
  -> Agent Runtime
  -> Turn Engine
  -> typed Run Events
  -> shared Run completion
  -> message persistence and asynchronous memory curation

HTTP RAG search or Agent context retrieval
  -> request or persisted Run Workspace scope
  -> Knowledge Base
  -> query embedding
  -> Retrieval Pipeline
       -> Store dense recall
       -> Store lexical recall
       -> deduplicate / prompt-injection guard / RRF fusion / rerank / relevance gate
       -> Context Selector
            -> scoped same-parent / adjacent chunk lookup
            -> prompt-injection guard / deduplicate / token-limit selection
       -> Context Transformer
            -> source deduplication / document grouping / adjacent merge
            -> final knowledge token-limit check

HTTP document ingestion
  -> Workspace scope resolution
  -> Knowledge Base
  -> chunking / embedding
  -> Store
```

These paths can be followed directly from:

- `apps/api/internal/httpapi/chat.go`
- `apps/api/internal/agent/runtime.go`
- `apps/api/internal/turn/`
- `apps/api/internal/knowledge/knowledge_base.go`
- `apps/api/internal/rag/retrieval.go`
- `apps/api/internal/contextassembly/assembler.go`

The call paths preserve these ownership boundaries:

| Boundary | Responsibility |
| --- | --- |
| Workspace scope | Resolves a non-empty request namespace, rejects conflicting explicit sources, persists scope on owned resources, and keeps Store operations scoped. It is an isolation key, not proof of caller identity. |
| Retrieval Pipeline | Owns dense/lexical recall ordering, prompt-injection filtering, rank-based fusion, reranking, and relevance gating. Stores apply scope filters and expose recall results without defining final ranking policy. |
| Context Selector | Expands gated child hits within the matched document, Workspace/metadata scope, and token limit. Ranked hits remain separate from the context sent to the model. |
| Context Transformer | Deduplicates sources, groups chunks by document, merges adjacent chunks, preserves contributing IDs, and reapplies the knowledge token limit. |
| Document ingestion | Normalizes source text and derives versioned hashes, section parents, and UTF-8 byte offsets before persistence. Store adapters preserve these values. |
| Run completion | Persists the assistant message, transitions the Run, flushes the terminal SSE event, and schedules conservative Memory Curation. All modes emit the same `domain.RunEvent` contract. |

Detailed retrieval algorithms and failure boundaries live in
[Knowledge / RAG](knowledge-rag.md). New executables should reuse the
composition root instead of copying dependency construction from `cmd/server`.

## Extension Points

| Change | Intended boundary |
| --- | --- |
| Add another model provider | provider adapter behind the existing model client contract |
| Add a retrieval algorithm | `rag` pipeline stage with versioned response/trace metadata |
| Add a reranker | implement `rag.Reranker`, report implementation/config identity, and inject it at the composition root |
| Change relevance policy | implement `rag.RelevanceGate`; keep confidence and filtering outside Reranker implementations |
| Add storage | implement the capability interfaces consumed by the composition root |
| Add an agent framework | executor adapter; keep Run, Event, Budget, and Store ownership in AgentFlow |
| Add a verifier | register one versioned verifier implementation with owned config normalization |
| Add a failure type | retain the subsystem type, implement `failure.Classified`, and expose only non-secret details |

A new abstraction is justified only when it removes real duplication or creates
a boundary with different ownership, lifecycle, or failure semantics.
