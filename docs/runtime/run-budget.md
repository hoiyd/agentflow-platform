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

Run Budget was introduced in Runtime Snapshot v4 and its current single-owner
protocol in v5. The current Snapshot version uses the frozen value even when
environment configuration changes. Older Runs remain
readable through Replay but are no longer resumable; they never inherit current
limits implicitly.

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
ID with different values is rejected. Postgres uses a transaction, a Run-scoped advisory lock, and a
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

Soft post-completion compaction, asynchronous Memory synchronization, conversation
title generation, and embeddings are auxiliary platform work. They continue to
use global concurrency/RPM/TPM controls but do not retroactively fail a completed
Run or use chat-model token pricing in its ledger.

## Usage Breakdown and Cache-aware Cost

Optional `breakdown` values are **subsets**, not extra tokens:
`cached_input_tokens` is included in prompt tokens and `reasoning_tokens` in
completion tokens. Context-window, TPM, and hard token limits still count the
full input/output. Absent counters mean unknown; a reported `0` means zero.

The Chat Completions adapter recognizes OpenAI's
`prompt_tokens_details.cached_tokens` / `completion_tokens_details.reasoning_tokens`
and DeepSeek's `prompt_cache_hit_tokens` / `prompt_cache_miss_tokens` aliases.
Conflicting aliases, malformed types, negative values, or subsets outside their
parent total yield `source: invalid_details` with no usable subsets. Totals are
preserved; unrecognized formats cannot authorize a discount. See the
[DeepSeek usage contract](https://api-docs.deepseek.com/api/create-chat-completion/).

Each logical call selects exactly one frozen quote:

- Use the selected Snapshot route's pricing when an input/output rate is
  positive or an explicit cached-input rate is supplied.
- Older metadata-only routes (both rates zero, cached rate absent) use the
  frozen Run Budget input/output rates, with source `run_budget`.
- If neither supplies a quote, cost is `unknown`, not proof of free usage.
  Token/call limits still apply; an unpriced request has no reliable monetary
  cap. `cost_unknown_entries` makes this visible in ledger totals and Replay.

Rates are integer **microdollars per million tokens**, supplied by the operator,
not fetched from the provider. Optional
`cached_input_per_million_tokens_micros` must be nonnegative and no greater than
the ordinary input rate; an explicit zero is a known free cached-input quote.
The source and rates are frozen into the route revision and saved with each
ledger entry. Changing current route/global prices does not reprice old Runs
or their resumed calls.

Reservation and output admission use the full-input rate, with **no anticipated
cache hit**. Settlement applies the cached-input quote only for valid,
provider-reported cache usage and a known cached-input price:

```text
cost = ceil((prompt - cached) * input_rate / 1,000,000)
     + ceil(cached * cached_input_rate / 1,000,000)
     + ceil(completion * output_rate / 1,000,000)
```

Without usable cache usage/pricing, `cached = 0` for the cost estimate, not for
the reported counter. Estimated usage never receives a cache discount.
Reasoning is already priced inside completion; it is not added a second time.
`cost_details` retains the quote, discount flag and reason (`provider_usage`,
`estimated_usage`, `cache_usage_unknown`, `cache_price_unknown`, or
`pricing_unknown`). Arithmetic saturates rather than overflowing a hard cap.

Physical attempt usage and breakdown are retained in `model.attempt_finished`
events, linked by `record_id` and logical `model_call_id` to Request Capture.
They do not increment the logical ledger. Retry and stream-usage fallback reuse
one reservation; only the final terminal result settles it. A failed attempt
without usage is unknown, and an incomplete stream leaves the reservation open
even if some usage arrived. The logical estimate cannot guarantee coverage of
all provider-billed failed attempts; it is **not an invoice**.

Replay's Resource usage panel has a collapsed **Model usage details** table with
per-call subsets, source and frozen quote. An unknown-priced call is shown as
Unknown; mixed totals show the priced subtotal plus unknown, not a zero bill.
New JSONB columns in `run_usage_entries` are added idempotently at Store startup;
existing rows retain unknown details. No new dashboard or billing store exists.

The CI usage gate runs `e2e/usage-breakdown.spec.ts` with
`AGENTFLOW_USAGE_TEST=1` and a dedicated `TEST_DATABASE_URL`. It retains JSON
evidence for all three modes, cache hit/zero/unknown/invalid details and reload.
Adapter integration tests separately cover non-streaming, retry, usage-option
fallback, incomplete streams and unknown failed-attempt billing. Resume tests
change live prices and check settlement still uses the saved quote; Postgres
round-trip tests check duplicate settlement and serialized explicit zero.

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

Since Snapshot v5, Autonomous iterations and output characters remain
mode-owned loop guards. Runtime and Tool configuration is resolved against the
general Run Budget once at Run creation, and the stricter values are stored only
in `RuntimeRunBudget`. The ledger/controller is therefore the sole runtime and
Tool enforcement owner; the Autonomous progress projection reads those frozen
values but does not run a second counter. Only the current Snapshot version is
resumable; earlier versions remain Replay-only.

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
The Run-scoped Tool Progress Guard separately detects repeated typed failures,
unchanged read-only results, and bounded alternating loops. Its default
`allow -> warn -> block_call -> halt_turn` policy runs before Tool Budget once a
stable repeated outcome has been established, so blocked calls do not consume
Budget or execute a Handler. See
[Tool Progress Guard](../tools/tool-progress-guard.md).

## Invariants Worth Testing

- Reservation and settlement for one operation are idempotent.
- Settlement replaces the reservation estimate in effective totals.
- An interrupted call leaves an observable conservative reservation.
- Postgres admission is atomic under concurrent writers.
- Waiting and queue time do not consume active runtime.
- Snapshot compatibility does not silently apply new limits to legacy Runs.

Focused commands are listed in [Manual tests](../operations/manual-tests.md).
