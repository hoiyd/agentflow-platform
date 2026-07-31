# Documentation Guide

AgentFlow documentation is organized by engineering question rather than by
source directory. Start with the path that matches the depth of review you need.

## Five-Minute Orientation

1. [Project README](../README.md): scope, architecture, capabilities, and known
   limitations.
2. [Interview demo](demo.md): repeatable three-to-five-minute walkthrough and
   fallback path.
3. [Engineering decisions](engineering-decisions.md): why the platform uses
   explicit runtime primitives and how the design evolved.
4. [Backend architecture](backend-architecture.md): package ownership,
   dependency direction, and main call paths.

## Runtime and Reliability

| Document | Question answered |
| --- | --- |
| [Internal terms](terms.md) | What do Conversation, Run, Stage, Turn, Iteration, and Model Call mean? |
| [Execution controls](execution-controls.md) | Which layer owns concurrency, rate limits, retries, budgets, context capacity, and stopping rules? |
| [Run Budget](run-budget.md) | How are logical calls, provider usage, tools, runtime, and cost accounted for? |
| [Context management](context-management.md) | How is each model input assembled, bounded, compacted, and explained? |
| [Completion verification](completion-verification.md) | How can a candidate output be checked before a Run is considered complete? |

## AI Context Systems

| Document | Question answered |
| --- | --- |
| [Knowledge / RAG](knowledge-rag.md) | How do ingestion, source tracking, hybrid recall, RRF, reranking, gating, and context transformation work? |
| [Memory management](memory-management.md) | Which conversation facts become durable semantic memory, and how is unsafe persistence avoided? |

## Operations and Interfaces

| Document | Question answered |
| --- | --- |
| [Backend configuration](backend-configuration.md) | Which environment variables configure providers, storage, limits, tools, and verification? |
| [API reference](api-reference.md) | Which HTTP endpoints and response contracts are available? |
| [Verification guide](verification-guide.md) | How can the major behaviors be reproduced manually or in integration tests? |
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
