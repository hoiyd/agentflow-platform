# Engineering Decisions

These are the trade-offs behind the implementation, not a feature history.
Each decision links to the contract and code path that makes it inspectable.

## Native Orchestration Owns the Execution Protocol

The Runtime is implemented directly in Go so Run/Stage ownership, model/Tool
rounds, cancellation, accounting and Resume share an explicit protocol. This is
a project scope choice, not a claim that frameworks are unnecessary.

**Cost:** more execution code to maintain. **Constraint:** one shared
[Turn Engine](../../apps/api/internal/agent/turn), narrow interfaces and common
contract tests; providers remain adapters below the Runtime.

## OpenAI-Compatible Does Not Mean Capability-Identical

Routes declare supported capabilities; adapters preserve required continuation
fields rather than treating display text as the entire response. Unsupported
Tool Calling is a typed terminal error, never executable text parsed from prose.

**Trade-off:** compatibility needs provider-specific evidence. Narrow transport
fallbacks, such as omitting unsupported streaming usage metadata, cannot disguise
quota/authentication errors. Retries reacquire physical-request permits and stop
after visible deltas. See [model routes](../runtime/model-routing.md) and
[Tool-loop protocol](../tools/bounded-tool-loop.md).

## One Execution Model for Single, Multi, and Loop

Modes own topology: direct Turn, approved staged collaboration, or bounded
iteration. Retrieval, Tools, Context, Events, Budget and Completion stay shared.
Copying a loop per mode would let policies drift while traces still look alike.

**Trade-off:** fixed orchestration is less flexible than arbitrary DAGs. It
makes ownership and recovery tractable; extra stages must earn their cost through
task evidence. Multi's Worker is an isolated Stage, not a parallel Child Run.
See [execution modes](../runtime/execution-modes.md).

## Retrieval Separates Ranking from Acceptance

Independent Keyword recall preserves exact identifiers missed by vector Top-K;
RRF combines ranks without equating raw score scales. A reranker orders evidence;
an independent relevance gate decides whether any evidence is acceptable.
Context expansion and merging do not change the child-hit evaluation target.

**Trade-off:** more stage metadata crosses API/Replay, but failures become
attributable. `no_match` can remove weak context without pretending that the
corpus has no answer. [RAG](../knowledge/knowledge-rag.md) and the
[Golden Dataset](../evaluation/rag-golden-dataset.md) expose the limits.

## Context Capacity Is Not a Run Budget

Context limits bound one model input. Run Budget bounds cumulative work.
Process/owner/key limits protect shared capacity; retries count physical attempts
without charging extra logical calls. Trace is not an atomic accounting source.

**Trade-off:** controls need distinct units and owners. The
[control map](../runtime/execution-controls.md) resolves precedence instead of
adding competing counters; the [Ledger](../runtime/run-budget.md) owns settlement.

## Freeze Protocol, Keep Deployment Policy Live

Snapshot freezes the protocol required to interpret a Run: configuration,
routes, Tool definitions, Context and Budget. Credentials, current access grants,
handlers, process capacity and operator security ceilings remain live.

**Trade-off:** old Runs remain readable, but only supported Snapshot schemas
are resumable. Resume cannot silently substitute live config or preserve revoked
authority. See [Snapshot compatibility](terms.md#runtime-snapshot).

## Durable Memory Requires Curation

Original Messages remain history. Memory Candidates provide a proposal layer
instead of promoting every model output or temporary instruction into long-term
facts. Accepted facts pass content/secret/confidence checks; optional adaptive
extraction defaults to shadow mode.

**Trade-off:** conservative curation can miss useful facts. Shadow measurements
can justify wider recall without silently widening trust.
[Task State](../runtime/task-state.md), historical summaries and
[Memory](../context/memory-management.md) retain different responsibilities.

## Completion Is a Policy Decision

Generating text is execution progress, not proof of task success. An optional
Completion Contract binds immutable Evidence to the exact candidate and gates
completion. Structural checks, embeddings and lexical support have different
failure meanings; none is a general truth oracle.

**Trade-off:** checks add latency and operational dependencies, so ordinary Chat
does not enable them by default. A blocked verifier never becomes an implicit pass.
See [Verification](../runtime/verification.md) and [evaluation](../evaluation/README.md).

## Observability Is Part of the Domain

Snapshot explains configuration; durable Events/receipts explain execution;
Usage Ledger explains accounting. Replay projects these records rather than
reconstructing state from logs. Optional Capture has a separate privacy policy.

**Trade-off:** durable evidence needs storage and retention. It makes uncertain
effects and failed recovery visible instead of claiming exactly-once execution.
See [reconciliation](../tools/tool-side-effect-reconciliation.md) and
[request reconstruction](../context/model-request-reconstruction.md).

## Deliberate Limits

Single-instance ownership is not distributed scheduling. Owner-scoped access is
not team/object-sharing ACLs. Deterministic fixtures are not general model-quality
evidence. Broader graders, durable Worker dispatch and retention/release hardening
remain separate work; [deployment boundaries](../operations/production-readiness-roadmap.md)
is the canonical status reference.
