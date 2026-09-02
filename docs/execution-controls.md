# Execution Controls and Resource Boundaries

This document is the canonical map of AgentFlow execution controls. When two
settings sound similar, compare their **scope**, **unit**, and single
**enforcement owner** before changing either one.

Business parameters such as search result count, upload size, or Memory
confidence remain in their subsystem documents. This page covers admission,
capacity, resource consumption, timeouts, and stopping conditions.

## Quick Diagnosis

| Symptom | Control to inspect | Do not confuse it with |
| --- | --- | --- |
| Too many tasks run concurrently | Run Admission | calls made by one Run |
| Provider concurrency or 429 pressure | Model Request Limiter | Run Budget |
| One Run consumes too many calls, tokens, or cost | Run Budget | RPM/TPM |
| One prompt does not fit the model context window | Context Assembly | cumulative Run tokens |
| Autonomous execution loops or expands output | Autonomous Loop Guards | Model Retry |
| A tool hangs, returns too much, or is unsafe to parallelize | Tool Execution Policy | Run concurrency |
| Verification retries or artifacts grow without bounds | Completion Contract and verifier boundaries | Model Retry |
| A crashed process leaves a Run in `running` | Recovery stale threshold | Run runtime budget |

## Control Types

| Type | Question answered | Typical behavior |
| --- | --- | --- |
| Admission | May work enter the system? | reject, queue, `Retry-After` |
| Concurrency | How much work may be active now? | semaphore, single-writer |
| Rate limit | How much work may occur over time? | token bucket, wait, reject |
| Retry policy | How many physical attempts may one logical operation use? | classify, back off, stop |
| Run budget | How much cumulative work may one Run consume? | reserve, settle, typed error |
| Capacity | Can one request fit a fixed-size resource? | select, compact, cap output |
| Timeout | How long may one operation wait? | context cancellation, typed timeout |
| Loop guard | When must an agent loop stop? | iteration or output limit |
| Observability | What already happened? | Run Events, Trace, Replay, and Episode Report; no enforcement |

## Ownership Matrix

| Control | Scope | Unit | Owner | Persisted? |
| --- | --- | --- | --- | --- |
| Run Admission | process + Conversation | active and queued Runs | `concurrency.RunController` | no |
| Child Run Admission | process + parent Run | active delegated Runs; no queue | `delegation.Controller` | relationship persisted; permits are not |
| Model Request Limiter | process + API key | physical HTTP requests and approximate input tokens | `concurrency.ModelRequestLimiter` | no |
| Model Retry | one logical Model Call | physical attempts | `openai.RetryPolicy` | no |
| Run Budget | one persisted Run | logical calls, provider tokens, tools, active runtime, cost | `budget.Tracker` + Usage Store | yes |
| Context Assembly | one logical Model Call | context tokens | `contextassembly.Assembler` | config frozen with Run |
| Loop Guard | one Loop (`autonomous`) Run | iterations and accumulated output characters | `autonomous` runtime | config frozen with Run |
| Tool Policy | one Tool Call or batch | timeout, bytes, parallel group, side-effect idempotency, artifact spill | `tools.Executor` | schema and side-effect declaration frozen; artifact metadata durable |
| Verification | one contracted Run | attempts, timeout, artifacts | `verification.Engine` | contract and evidence |
| Recovery | startup scan + Resume | stale lifecycle, Stage checkpoints, Tool effects | `recovery` + `checkpoint` | recovery state persisted; threshold live |
| Observability | one Run | Run Events and derived Trace, Replay, and Episode Report views | Event Store / projection builders | durable events only; projections do not enforce policy |

## 1. Run Admission and Conversation Concurrency

```env
MAX_CONCURRENT_RUNS=8
RUN_QUEUE_SIZE=32
RUN_QUEUE_WAIT_TIMEOUT=30s
```

- `MAX_CONCURRENT_RUNS` limits active Agent Runs in one process.
- `RUN_QUEUE_SIZE` adds bounded waiting capacity beyond active slots.
- `RUN_QUEUE_WAIT_TIMEOUT` limits waiting for a conversation writer or global
  slot.
- One `conversation_id` remains single-writer even when global capacity exists.
- A full queue returns `429`; an expired wait returns `503`. Both include
  `Retry-After`.

Admission does not count Model Calls or limit the number of steps inside an
admitted Run.

Child Runs use a separate, no-wait admission boundary:

```env
MAX_CONCURRENT_CHILD_RUNS=2
MAX_CHILD_RUNS_PER_PARENT=1
CHILD_RUN_TIMEOUT=2m
```

These values do not increase `MAX_CONCURRENT_RUNS` or create another queue.
Child model calls still acquire the global Model Request Limiter. Child Budget
and bounded result settings are documented in
[Bounded Child Run Delegation](child-run-delegation.md).

## 2. Model Request Limiter

```env
MAX_CONCURRENT_MODEL_REQUESTS=8
MODEL_REQUESTS_PER_MINUTE=60
MODEL_TOKENS_PER_MINUTE=120000
```

- Concurrency counts physical model HTTP requests currently in flight.
- Chat and Embedding share the limiter; a stream holds its slot until the body
  closes.
- RPM and approximate input TPM use per-API-key token buckets.
- Every retry is another physical request and consumes a new permit.
- Backoff does not hold a concurrency slot.
- With no API key, no per-key bucket is created; real HTTP work still uses the
  global concurrency control.

A request larger than total TPM bucket capacity returns
`request_token_capacity_exceeded`. That is not a Run Budget error.

## 3. Provider Timeout and Retry

```env
OPENAI_REQUEST_TIMEOUT=5m
MODEL_RETRY_MAX_ATTEMPTS=3
MODEL_RETRY_BASE_DELAY=500ms
MODEL_RETRY_MAX_DELAY=5s
```

- Request timeout applies to one physical provider attempt.
- Maximum attempts includes the initial request; `1` disables retries.
- Base and maximum delay control exponential backoff; the maximum also caps a
  provider `Retry-After` value.
- Transport failures, timeouts, rate limits, and provider `5xx` errors are
  retryable.
- Authentication, quota, model-not-found, invalid request, context length,
  content policy, and cancellation errors fail immediately.
- Streaming retries only before the first delta, preventing duplicated output.

One logical Model Call may contain several attempts. Attempts count against
RPM/TPM; the entire Retry Policy uses one Run Budget reservation.

## 4. Run Budget and Usage Ledger

```env
RUN_MAX_MODEL_CALLS=32
RUN_MAX_PROMPT_TOKENS=200000
RUN_MAX_COMPLETION_TOKENS=50000
RUN_MAX_TOTAL_TOKENS=250000
RUN_MAX_TOOL_CALLS=50
RUN_MAX_RUNTIME=15m
RUN_MAX_ESTIMATED_COST_USD=0
MODEL_INPUT_COST_PER_MILLION_TOKENS_USD=0
MODEL_OUTPUT_COST_PER_MILLION_TOKENS_USD=0
```

Run Budget limits cumulative resources for one persisted Run and is frozen in
its Runtime Snapshot. Configuration changes affect new Runs only.

| Dimension | Accounting rule |
| --- | --- |
| Model calls | logical operations; provider retries do not add calls |
| Prompt tokens | estimated reservation followed by provider settlement |
| Completion tokens | provider usage; also constrains per-request `max_tokens` |
| Total tokens | cumulative prompt + completion |
| Tool calls | admitted valid calls; handler errors and timeouts still count |
| Runtime | accumulated `running` segments; queue and human wait do not count |
| Estimated cost | frozen prices calculated in integer microdollars |

Zero disables one dimension. Price configuration becomes an enforced limit only
when `RUN_MAX_ESTIMATED_COST_USD` is positive.

The Usage Ledger is authoritative for enforcement. Trace Summary and Episode
are observational projections. See [Run Budget and Usage Ledger](run-budget.md)
for reservation, settlement, and overage semantics.

## 5. Context Assembly and Compaction

```env
MODEL_CONTEXT_WINDOW_TOKENS=128000
MODEL_OUTPUT_RESERVE_TOKENS=8192
CONTEXT_SAFETY_MARGIN_TOKENS=4096
CONTEXT_HISTORY_MAX_TOKENS=64000
CONTEXT_MEMORY_MAX_TOKENS=8000
CONTEXT_KNOWLEDGE_MAX_TOKENS=16000
CONTEXT_TOOL_RESULT_MAX_TOKENS=2000

CONTEXT_COMPACTION_MODE=auto
CONTEXT_COMPACTION_SOFT_THRESHOLD=0.70
CONTEXT_COMPACTION_HARD_THRESHOLD=0.85
CONTEXT_COMPACTION_RECENT_TOKENS=16000
CONTEXT_COMPACTION_SUMMARY_MAX_TOKENS=2000
CONTEXT_COMPACTION_TIMEOUT=45s
```

One request has this input capacity:

```text
context window - output reserve - safety margin
```

History, Memory, Knowledge, and Tool Result limits are per-source input caps,
not cumulative Run budgets. Each assembly emits a Context Manifest explaining
selection, exclusion, transformation, and token estimates.

Both output reserve and remaining Run completion capacity affect provider
`max_tokens`; the stricter value wins. One protects a single request, while the
other protects cumulative Run usage.

Soft Compaction runs asynchronously after completion. Hard Compaction is Turn
preflight work and is recorded with `compaction` purpose in the Run Ledger. See
[Context Management](context-management.md).

## 6. Autonomous Loop Guards

```env
AUTONOMOUS_MAX_ITERATIONS=5
AUTONOMOUS_MAX_RUNTIME_SECONDS=300
AUTONOMOUS_MAX_OUTPUT_CHARS=60000
AUTONOMOUS_MAX_TOOL_CALLS=20
```

- Iterations and accumulated output characters are owned by the Autonomous
  loop.
- One iteration normally contains Observe, Plan, Act, Review, and Decide; it is
  not equivalent to one Model Call.
- For new Snapshot v5 Runs, mode-specific runtime/tool values and general Run
  Budget values are resolved to the stricter effective value at creation.
  Run Budget then becomes the single enforcement owner for those resources.
- Snapshot v4 and older Runs resume under their historical protocol.

With defaults, the effective Autonomous runtime/tool caps are `5m/20`, not the
general `15m/50`. Run Budget limits quantity; a future Progress Guard must
detect repetition, oscillation, and lack of progress.

## 7. Tool Execution Policy

| Control | Default | Scope |
| --- | --- | --- |
| Execution timeout | 30s | one Tool Call |
| Maximum argument schema | 65,536 bytes | one registered Tool |
| Maximum arguments | 65,536 bytes | one Tool Call |
| Maximum result | 20,000 bytes | one Tool Result |
| `TOOL_RESULT_MAX_BATCH_BYTES` | 8,000 bytes | one source-ordered Tool batch |
| `TOOL_ARTIFACT_MAX_BYTES` | 5 MiB | one persisted Tool Artifact |
| `TOOL_ARTIFACT_PREVIEW_BYTES` | 1,000 bytes | one model-visible spill preview |
| Batch concurrency | 4 workers | one Tool batch |

Catalog registration normalizes and compiles each parameter schema as JSON
Schema 2020-12. The normalized schema is the single source used for the model
definition, argument validation, definition revision, and Runtime Snapshot.
Unsupported drafts, remote references, non-object roots, invalid schemas, and
oversized schemas fail registration.

Before budget accounting or handler execution, Tool arguments are bounded,
decoded once, canonicalized, validated against that compiled contract, and
authorized against the frozen Tool Security Policy. Policy rejection therefore
does not consume Tool Budget or create a side effect. See
[Tool Security Policy and Scope](tool-security-policy.md).
Validation failures return a stable code, JSON Pointer path, and non-secret
message. The canonical argument hash and frozen definition revision are reused
by tracing and the side-effect journal.

Offline Tool selection evaluation calls the same side-effect-free Catalog
validation path, so its schema decisions cannot drift from Executor behavior.
Every production Binding is covered by a shared contract/sensor suite, while a
deterministic fault harness exercises timeout, cancel, panic, result limits,
Budget denial, tracing, and effect-journal boundaries. See
[Tool Contract and Fault Testing](tool-contract-testing.md).

Tool name, description, normalized parameter schema, schema version, definition
revision, capability, and operator policy are frozen with new Snapshot v11
Runs. Snapshot v10 remains resumable with the current fail-closed policy. The live Binding owns
handler, timeout, result-size, and concurrency policy. Execution is serial
unless a Binding declares a safe `read_only` or keyed parallel group. Oversized
results are redacted and persisted as immutable Tool Artifacts; model Context
receives a UTF-8-safe bounded preview and opaque recovery reference. Batch
accounting includes both raw results and previews, so adding more Tools cannot
silently multiply Context usage. See [Tool Result Artifact Governance](tool-result-artifacts.md).

Tool timeout bounds one handler. Run runtime budget bounds cumulative active
execution; neither substitutes for the other.

Bindings that write external state must declare `side_effect.mode=external`.
They execute behind a durable idempotency journal: committed results replay
without reinvoking the handler, while uncertain attempts require explicit
reconciliation. See [Durable Recovery and Stage Checkpoints](durable-recovery.md).

## 8. Verification

Verification runs only when the initial request includes a
`completion_contract`.

| Control | Default / range | Scope |
| --- | --- | --- |
| Verifier timeout | 30s; 1ms-5m | one verifier |
| Policy attempts | 2; 1-5 | one Completion Contract |
| Maximum artifacts | 8 | one Evidence record |
| `VERIFICATION_MAX_ARTIFACT_BYTES` | 65,536 bytes | one persisted artifact |

Verifier attempts are not Model Retries and do not increase model-call usage;
current built-in verifiers do not call a model. Contract and policy are frozen
with the Run.

## 9. Recovery Stale Threshold

```env
RECOVERY_STALE_RUN_TIMEOUT=60s
```

Startup recovery repairs open Tool, Model, Turn, and Stage lifecycles before
atomically marking a stale `running` Run `failed_recoverable`. Resume validates
durable Stage checkpoints against the frozen Runtime Snapshot and Tool
definitions. This is neither an execution timeout nor a Run Budget value. See
[Durable Recovery and Stage Checkpoints](durable-recovery.md).

## Frozen Protocol vs. Live Policy

| Frozen with Run | Live process policy |
| --- | --- |
| Run Budget | Run and model concurrency |
| Context Assembly and Compaction config | queue size and wait timeout |
| Autonomous iteration/output config | RPM/TPM buckets |
| provider/model identity | retry, backoff, and HTTP timeout |
| tool name, description, parameters, side-effect declaration | tool handler, timeout, result, concurrency |
| Completion Contract | recovery stale threshold |

Frozen values keep Resume and Replay stable. Live values protect the current
process and provider without rewriting historical execution protocol.

## Common Category Errors

1. **RPM is not model-call budget.** Retry increases RPM but not logical calls.
2. **TPM is not cumulative Run tokens.** TPM is a time-window input estimate;
   the ledger settles provider input and output usage.
3. **Context Window is not cost budget.** It only determines whether one input
   fits.
4. **Iteration is not Model Call.** One iteration normally makes several calls.
5. **Timeout is not runtime budget.** Operation duration and cumulative active
   runtime have different owners.
6. **Trace is not Ledger.** Trace explains; Ledger admits and accounts.
7. **Multiple policy inputs require one owner.** Resolve precedence at snapshot
   creation instead of maintaining two counters for the same resource.

## Tuning Order

1. Set Context Assembly from the real model context window.
2. Set RPM/TPM and model-request concurrency from provider limits.
3. Set Run concurrency and queue from machine capacity.
4. Set Run Budget from acceptable cost and failure radius.
5. Set Autonomous iteration/output profile; runtime/tool caps fold into Run
   Budget.
6. Tune Tool, Verification, and Recovery operation timeouts last.

Change one layer at a time and inspect Usage, Replay, and Run Events before
adjusting another.

## Checklist for a New Control

Before adding a limit, timeout, quota, or guard, answer:

1. Is its scope request, attempt, Model Call, Turn, Run, Conversation, API key,
   or process?
2. Is its unit count, token, byte, duration, cost, or concurrency slot?
3. Which package is the single enforcement owner?
4. Is it checked before admission, during execution, or after settlement?
5. Is configuration frozen with the Run or live deployment policy?
6. What do zero, negative, and missing values mean?
7. Which typed error, HTTP/SSE response, and Run Event explain rejection?
8. How do Replay and Usage expose it, and which test prevents double counting?

Update this document, `.env.example`, `internal/config.Config`, and boundary
tests in the same change. If an existing control owns the same resource, merge
policy inputs or define precedence instead of adding another runtime counter.
