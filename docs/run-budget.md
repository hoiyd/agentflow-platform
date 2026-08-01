# Run Budget and Usage Ledger

For a system-wide comparison with concurrency, RPM/TPM, Retry, Context, Loop,
Tool, and Verification controls, see
[Execution Controls](execution-controls.md).

Run Budget bounds cumulative work attributable to one Run. It complements the
shared concurrency and provider controls rather than replacing them.

The key design choice is to separate **logical work** from **physical provider
attempts**. A retried HTTP request consumes provider rate-limit capacity, but it
does not become another logical model call in the Run ledger.

## Control Boundaries

| Control | Scope | Counts |
| --- | --- | --- |
| Run admission | process and conversation | active Runs and queued Runs |
| Model request limiter | API key and process | physical HTTP attempts, RPM, and approximate TPM |
| Retry Policy | one logical model call | retryable physical attempts and backoff |
| Run Budget | one persisted Run | logical model calls, tokens, tools, active runtime, and configured cost |
| Context Assembly | one logical model call | context-window fit and output reserve, not cumulative usage |
| Loop guards | one Loop (`autonomous`) Run | iterations and accumulated output characters |
| Trace Summary / Episode Report | one persisted Run | observational projection only; never admission or enforcement |

A retry acquires another model-request permit but does not reserve another Run
model call. The reservation surrounds the entire Retry Policy operation.

## Frozen Budget

New Runs store `RuntimeRunBudget` in Runtime Snapshot v5. Resume and Replay use
that frozen value even when environment configuration changes. Snapshot v1-v3
Runs remain readable and resumable under their original, budgetless protocol;
they never inherit current limits implicitly.

Configured dimensions are:

- logical model calls;
- prompt, completion, and total tokens;
- admitted tool executions;
- active runtime;
- estimated model cost in integer microdollars.

Zero disables a dimension. API keys and credentials are never stored.

## Accounting Model

Each model call has a stable `operation_id`, normally the Context Manifest
`model_call_id`:

1. A reservation records one logical call, estimated prompt usage, one minimum
   output token, and estimated cost before provider access.
2. Provider retries reuse that reservation.
3. A settlement records absolute provider usage for the same operation.
4. Effective totals use settlement instead of reservation. A call interrupted
   before settlement remains visible as an open conservative reservation.

Duplicate reservation or settlement writes are idempotent. Reusing an operation
ID with different values is rejected. File Store serializes the check and append
under its mutex. Postgres uses a transaction, a Run-scoped advisory lock, and a
unique `(run_id, operation_id, kind)` constraint.

Tool budget is charged after catalog lookup and JSON-object validation but
before handler execution. Unknown tools and malformed arguments therefore do
not consume the execution budget. Handler errors and timeouts do consume it
because execution was admitted.

## Usage Purpose

The ledger classifies in-Run model work as:

- `primary`: normal Agent stages, tool selection, revision, and final response;
- `router`: LLM-backed agent routing;
- `compaction`: hard preflight Context Compaction required by the active Turn.

Soft post-completion compaction, asynchronous Memory Curation, conversation
title generation, and embeddings are auxiliary platform work. They continue to
use global concurrency/RPM/TPM controls but do not retroactively fail a completed
Run or use chat-model token pricing in its ledger.

## Hard and Observed Enforcement

Model-call, prompt-estimate, tool-call, and active-runtime limits are checked
before work is admitted. Remaining completion, total-token, and configured-cost
capacity is converted into a per-request `max_tokens` cap and combined with the
Context Assembler output reserve by taking the stricter value.

Provider usage remains authoritative. A provider may tokenize differently or
ignore a requested output cap, so settlement is always persisted before an
overage error is returned. Streaming output or provider cost may already have
occurred at that point; the ledger records this observed overage rather than
pretending it was prevented.

## Active Runtime and Autonomous Limits

Run state persists `active_runtime_ms` and the current execution-segment start.
Transitions out of `running` close the segment. Time spent queued,
`waiting_for_user`, canceling, or stopped does not consume runtime budget.

For new Snapshot v5 Runs, Autonomous iterations and output characters remain
mode-owned loop guards. Its runtime and tool configuration is resolved against
the general Run Budget once at Run creation, and the stricter values are stored
only in `RuntimeRunBudget`. The ledger/controller is therefore the sole runtime
and tool enforcement owner; the Autonomous progress projection reads those
frozen values but does not run a second counter.

Snapshot v1-v3 Autonomous Runs have no Run Budget. Snapshot v4 introduced Run
Budget but still carried the original Autonomous runtime/tool guards. Resume
preserves those historical protocols so an old Run does not silently change;
only newly captured v5 Runs use the single-owner model.

## Events and API

Successful ledger appends emit `usage.recorded`. Rejected reservations and
observed settlement/runtime overages emit `budget.exceeded` with resource,
limit, used, requested, operation ID, and purpose.

```text
GET /api/runs/{id}/usage
GET /api/runs/{id}/replay
```

Replay preserves Context metadata, Verification Evidence/Artifacts,
Run Events, and the same Usage Ledger.

## Progress Guard

Run Budget limits quantity; it does not decide whether work is making progress.
Repeated tool signatures, repeated results/errors, oscillation detection, and
`warn -> block_call -> halt_turn` behavior belong to a separate Progress Guard.

## Invariants Worth Testing

- Reservation and settlement for one operation are idempotent.
- Settlement replaces the reservation estimate in effective totals.
- An interrupted call leaves an observable conservative reservation.
- Postgres admission is atomic under concurrent writers.
- Waiting and queue time do not consume active runtime.
- Snapshot compatibility does not silently apply new limits to legacy Runs.

Focused commands are listed in [Manual tests](manual-tests.md).
