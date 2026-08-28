# Documentation Guide

AgentFlow documentation is organized by engineering question rather than by
source directory. Start with the path that matches the depth of review you need.

## Five-Minute Orientation

1. [Project README](../README.md): scope, architecture, capabilities, and known
   limitations.
2. [Interview demo](demo.md): repeatable three-to-five-minute walkthrough and
   fallback path.
3. [Execution modes](execution-modes.md): how Single, Multi, and Loop differ,
   when to choose each, and which runtime contracts they share.
4. [Agent profiles](agent-profiles.md): how custom prompts, Tool/RAG/Memory
   policy, executors, Router candidates, and frozen Run semantics fit together.
5. [Engineering decisions](engineering-decisions.md): why the platform uses
   explicit runtime primitives and how the design evolved.
6. [Backend architecture](backend-architecture.md): package ownership,
   dependency direction, and main call paths.

## Runtime Systems Review

For a focused backend or AI-systems review, use this path:

| Topic | Start here | What to inspect |
| --- | --- | --- |
| **Performance** | [Execution controls](execution-controls.md) | Streaming, bounded work, context/output capacity, Run budgets, timeouts, and tuning order. The project makes no unsupported benchmark claim. |
| **Concurrency** | [Run admission and Conversation concurrency](execution-controls.md#1-run-admission-and-conversation-concurrency) | Global admission, bounded queueing, per-Conversation single-writer behavior, model-request permits, RPM/TPM, and retry slot ownership. |
| **Tracing** | [Backend architecture](backend-architecture.md#performance-concurrency-tracing-and-verification) | Typed lifecycle events, persisted payloads, Usage Ledger separation, Replay, and Episode projections. |
| **Verification** | [Verification](verification.md) | Runtime outcome contracts, versioned verifiers, immutable Evidence/Artifacts, and the Completion Gate. This is separate from Automated and Manual Tests. |

The same runtime contracts apply to Single, Multi, and Loop execution. The API
names the Loop path `autonomous`. File/Postgres stores and provider/framework
adapters preserve those contracts, which is the main portability boundary of
the project.

## Runtime and Reliability

| Document | Question answered |
| --- | --- |
| [Agent profiles](agent-profiles.md) | How are reusable Agent personas, prompts, Tool permissions, retrieval policy, executors, and Router candidates configured and frozen? |
| [Execution modes](execution-modes.md) | How do Single, Multi, and Loop execution differ in lifecycle, checkpoints, trace shape, cost, and use case? |
| [Internal terms](terms.md) | What do the execution entities mean, and how do Run Events, Trace, Replay, and Episode Report differ? |
| [Execution controls](execution-controls.md) | Which layer owns concurrency, rate limits, retries, budgets, context capacity, and stopping rules? |
| [Failure handling](failure-handling.md) | How do subsystem errors retain specific types while exposing one structured trace and Episode contract? |
| [Run Budget](run-budget.md) | How are logical calls, provider usage, tools, runtime, and cost accounted for? |
| [Durable recovery](durable-recovery.md) | How are interrupted lifecycles repaired, Stage boundaries checkpointed, and external Tool effects replayed safely? |
| [Bounded Child Run Delegation](child-run-delegation.md) | How does Multi-Agent isolate Worker authority, context, budget, cancellation, result size, and parent-child Replay topology? |
| [Context management](context-management.md) | How is each model input assembled, bounded, compacted, and explained? |
| [Structured Task State](task-state.md) | How do goals, tasks, decisions, constraints, blockers, and Artifact references remain versioned across Runs and compaction? |
| [Model request reconstruction](model-request-reconstruction.md) | How are final model payloads hashed, optionally captured, and checked against Runtime Snapshot and Context Manifest records? |
| [Event projections and runtime invariants](event-projections-runtime-invariants.md) | How are typed events converted into one read model, checked as an executable protocol, and handed off to live subscribers without a read/subscribe gap? |
| [Verification](verification.md) | How can a candidate output be checked before a Run is considered complete? |

## AI Context Systems

| Document | Question answered |
| --- | --- |
| [Knowledge / RAG](knowledge-rag.md) | How do ingestion, source tracking, hybrid recall, RRF, reranking, gating, and context transformation work? |
| [Golden Dataset v1 schema](schemas/rag-golden-dataset-v1.schema.json) | Which versioned fields define answerable, expected-source, forbidden-source, and tagged RAG evaluation cases? |
| [RAG Golden Dataset v1](rag-golden-dataset.md) | Which canonical retrieval cases and corpus exercise facts, exact IDs, multi-source recall, no-answer behavior, ACL/staleness leakage, and injection filtering? |
| [Memory management](memory-management.md) | Which conversation facts become durable semantic memory, and how is unsafe persistence avoided? |
| [Model request reconstruction](model-request-reconstruction.md) | What did a physical provider attempt actually receive, and when is its content retained? |

## Operations and Interfaces

| Document | Question answered |
| --- | --- |
| [Production readiness roadmap](production-readiness-roadmap.md) | Which implemented controls form the baseline, what blocks a controlled production beta, and what is deliberately deferred? |
| [Backend configuration](backend-configuration.md) | Which environment variables configure providers, storage, limits, tools, Verification, and the mandatory Workspace namespace? |
| [API reference](api-reference.md) | Which HTTP endpoints and response contracts are available? |
| [Manual tests](manual-tests.md) | How can the major behaviors be tested manually? |
| [Frontend design principles](../frontend_uex_design.md) | Which product and interaction constraints guide the workbench UI? |
| [Stylesheet organization](../apps/web/app/styles/README.md) | Where should frontend style changes be made? |

## Reading the Evidence

Documentation claims should be traceable to one of four forms of evidence:

- a persisted domain contract such as Runtime Snapshot, Context Manifest,
  Usage Ledger, Completion Contract, or Verification Evidence;
- a typed Run Event visible in Replay;
- an API response exposing versioned metadata;
- a focused test that exercises the boundary or failure case.

When implementation and documentation disagree, treat the implementation and
tests as authoritative, then update the affected document in the same change.
