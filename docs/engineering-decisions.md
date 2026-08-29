# Engineering Decisions and Project Evolution

This document explains the decisions that are easiest to miss when reading the
feature list. It focuses on boundaries, trade-offs, and the sequence in which
the platform learned to handle more realistic agent workloads.

## Native Orchestration Owns the Execution Protocol

AgentFlow implements its orchestration primitives directly in Go and uses one
native Turn Engine across every execution mode.

This is not an argument that frameworks are unnecessary. The project uses the
native path to make the underlying contracts inspectable:

- Run lifecycle and recovery;
- Stage and Turn ownership;
- model/tool loops;
- context assembly;
- typed events and replay;
- budget accounting;
- completion policy.

If a framework owned those concerns, the project could demonstrate framework
usage but would provide less evidence that its failure modes were understood.
Provider adapters remain below the Turn Engine and cannot redefine persistence,
observability, or safety policy.

**Trade-off:** more platform code must be maintained. The countermeasure is a
small shared Turn Engine, capability-oriented packages, and tests around common
contracts rather than separate implementations per mode.

## OpenAI-Compatible Does Not Mean Capability-Identical

OpenAI-compatible providers often implement the same endpoint shape but differ
in optional behavior. AgentFlow handles two known capability boundaries
explicitly:

- if native Tool Calling is rejected as unsupported, the Turn records the
  failure, rebuilds context without Tool definitions, and falls back to a
  constrained JSON Tool Call protocol;
- if streaming usage metadata is unsupported, the client retries the stream
  without `stream_options.include_usage` while retaining one logical usage
  reservation.

Ordinary transport and provider failures still follow typed retry policy.
Streaming retries stop after the first emitted delta so a failed physical
request cannot duplicate user-visible output. Each physical attempt reacquires
request concurrency and rate-limit permits; waits do not hold an in-flight
slot.

**Trade-off:** capability detection relies on classified provider errors and
must evolve as adapters expose new variants. The fallback is deliberately
narrow: authentication, quota, content policy, malformed requests, and other
terminal errors are never disguised as missing capabilities.

## One Execution Model for Single, Multi, and Loop

Single, Multi, and Loop modes differ in orchestration:

- Single executes one direct Turn.
- Multi creates planner, router, worker, reviewer, and finalizer Stages.
- Loop (`autonomous` in the API) repeats observe, plan, act, review, and decide Stages within
  explicit limits.

They do not implement separate Retrieval, Tool, Context, Event, Budget, or
Completion stacks. Shared behavior prevents a feature from working in one mode
while silently bypassing policy in another.

The complete lifecycle, human checkpoints, trace shape, limits, and selection
guidance are documented in [Execution modes](execution-modes.md).

**Rejected shortcut:** copy a chat loop for each mode and normalize only the UI.
That makes traces look consistent while runtime semantics drift.

## Retrieval Evolved by Separating Concerns

The RAG path was built as a sequence of narrower contracts:

1. A shared Retrieval Pipeline replaced direct Store calls from runtime code.
2. Keyword recall became independent from semantic recall so exact identifiers
   could enter the candidate set even when vector Top-K missed them.
3. Reciprocal Rank Fusion combined ranks rather than comparing incompatible raw
   score scales.
4. Reranking and a relevance gate separated candidate ordering from the policy
   decision to return no confident match.
5. Ranked child hits were separated from `context_items`, allowing parent and
   adjacent expansion without changing evaluation semantics.
6. Source traceability, deduplication, document grouping, and adjacent merging
   made the selected context auditable and less repetitive.

Each stage keeps version and ranking metadata in API and trace output. This
makes parameter changes reproducible, supports the current Golden Dataset v1
evaluation, and leaves threshold calibration auditable.

**Trade-off:** more metadata crosses the API. The benefit is that retrieval
quality can be debugged by stage instead of reduced to one opaque final score.

## Context Capacity Is Not a Run Budget

Agent systems contain several limits that all mention tokens or time. AgentFlow
keeps their scopes separate:

- provider RPM/TPM protects a shared API key over time;
- Run Budget limits cumulative logical work for one persisted Run;
- Context Assembly makes one model input fit its context window;
- Autonomous guards stop a repeating loop;
- operation timeouts bound one provider, tool, or verifier call.

The owner of each control is documented in
[Execution controls](execution-controls.md). When two configuration inputs
affect one resource, they are resolved into one effective owner instead of
maintaining competing counters.

**Rejected shortcut:** use trace totals as the budget authority. Traces are an
observational projection and cannot provide atomic admission or idempotent
settlement.

## Freeze Protocol, Keep Deployment Policy Live

A Run stores a Runtime Snapshot containing the protocol required for Resume and
Replay: mode, agent configuration, model identity, tool schemas, context policy,
and Run Budget. Credentials, process concurrency, rate-limit buckets, current
tool handlers, and recovery thresholds stay live.

This split preserves historical behavior without freezing secrets or preventing
operators from changing deployment capacity.

**Trade-off:** old Snapshot versions require compatibility code. AgentFlow
prefers explicit legacy behavior over silently applying a new policy to an old
Run.

## Durable Memory Requires Curation

Copying every chat message into a vector store is simple but causes temporary
instructions, model output, errors, and possible secrets to become long-term
memory. AgentFlow instead treats raw Messages as authoritative history and uses
Memory Candidates as an auditable proposal layer.

Explicit durability phrases take a deterministic fast path. Optional adaptive
model extraction defaults to shadow mode, where proposals can be evaluated
without affecting recall. Accepted facts still pass confidence, content, size,
and secret checks before embedding.

**Trade-off:** the system may forget facts that were not stated clearly enough.
That is preferable to silently persisting unsafe or low-quality memory; recall
can be widened after measuring shadow candidates.

## Completion Is a Policy Decision

A model returning text does not necessarily mean the requested outcome is
valid. AgentFlow therefore supports an optional frozen Completion Contract.
Deterministic verifiers produce immutable Evidence bound to the candidate output
hash and Runtime Snapshot. Only fresh evidence can open the Completion Gate.

Verification is opt-in because not every conversation needs the latency or
strictness of a gate. A blocked verifier never becomes an implicit pass.

**Current boundary:** structural checks, read-only HTTP probes, and allowlisted
commands can prove configured invariants. Subjective correctness still requires
calibrated model graders or human review.

## Observability Is Part of the Domain

Replay is built from durable typed Run Events, persisted Messages,
Collaboration Steps, Runtime Snapshot, Usage Ledger, and Verification Evidence.
These records are created at the ownership boundary where the action occurs;
they are not inferred later from logs.

This enables three different questions to be answered independently:

- **What was configured?** Runtime Snapshot and Completion Contract.
- **What happened?** Run Events, Steps, tool/model traces, and artifacts.
- **What was charged?** Usage Ledger reservations and settlements.

Keeping those views separate prevents a convenient UI projection from becoming
an accidental source of truth.

## What Remains Deliberately Unfinished

- Identity-derived Workspace lifecycle, Membership/ACL authorization, and
  multi-tenant release validation. Mandatory namespace filtering is already
  implemented, but it is not an authorization boundary by itself.
- Immutable Dataset storage/changelog, persisted Evaluation Runs, and calibrated
  retrieval/no-answer thresholds.
- Semantic prompt-injection classification beyond deterministic high-precision
  patterns and trust boundaries.
- Progress guards for repeated tool signatures, oscillation, and stalled loops.
- Asynchronous human evidence ingestion and calibrated model-based graders.

These are represented as explicit boundaries because extending a platform safely
starts with knowing which guarantees it does not yet provide.
