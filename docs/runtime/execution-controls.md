# Execution Controls and Resource Boundaries

Compare **scope, unit, and enforcement owner**, not similarly named settings.
This is the canonical control map. Defaults are in
[`.env.example`](../../apps/api/.env.example); startup configuration is in
[backend configuration](../operations/backend-configuration.md). Search counts,
upload limits, and Memory confidence belong to their subsystem contracts.

## Quick Diagnosis

| Symptom | Inspect | Not the same as |
| --- | --- | --- |
| Too many tasks active / waiting | Run admission | Calls inside a Run |
| In-flight requests or provider 429 pressure | Model limiter / provider retry | Run Budget |
| One user's Workspaces consume shared request capacity | Owner model admission | Workspace quota or monthly token budget |
| No model meets required capabilities | Route catalog | Transient retry |
| One Run consumes too much | Run Budget | RPM/TPM |
| One input does not fit | Context Assembly | Cumulative Run tokens |
| Agent repeats work | Loop / Tool Progress Guard | Model Retry |
| Tool hangs, spills, or is unsafe in parallel | Tool Executor | Run concurrency |
| Verification cannot pass / artifacts grow | Completion Contract | Model Retry |
| Crashed Run remains open | Recovery | Active-runtime cap |

## Ownership Matrix

| Control | Scope and unit | Enforcement owner | Persistence |
| --- | --- | --- | --- |
| Run admission | Process + Conversation; active/queued Runs | `concurrency.RunController` | Live process policy |
| Model limiter | Process + owner + API key; physical requests, pending attempts, estimated input tokens/minute | `concurrency.ModelRequestLimiter` | Live buckets/permits; owner resolved from Session/Run and Workspace |
| Model routing | Run; eligible target for one logical call | `routing.Catalog` | Frozen contracts and affinity |
| Retry | Logical Model Call; physical attempts | `openai.RetryPolicy` | Live process policy |
| Run Budget | Run; calls, tokens, Tools, active time, estimated cost | `budget.Tracker` + Usage Store | Frozen budget; durable ledger |
| Context | Model Call; input/output capacity | `contextassembly` | Frozen config; Manifest per assembly |
| Loop guard | Autonomous Run; iterations/output characters | Autonomous runtime | Frozen config |
| Tool execution | Call/batch; scope, time, bytes, concurrency, idempotency | `tool.Executor` | Frozen definitions/policy; durable receipts/Artifacts |
| Tool progress | Run; repeated failures, unchanged reads, oscillation | `progress.Guard` | Frozen thresholds; durable decisions |
| Verification | Contracted Run; attempts/time/artifacts | `verification.Engine` | Frozen contract; immutable Evidence |
| Recovery | Startup/Resume; stale lifecycle and checkpoints | `recovery` + `checkpoint` | Durable state; live stale threshold |
| Observability | Run; events and derived views | Event Store / projection builders | Durable facts; no policy enforcement |

## 1. Run Admission and Conversation Concurrency

`MAX_CONCURRENT_RUNS` caps active Runs. `RUN_QUEUE_SIZE` is additional waiting
capacity; `RUN_QUEUE_WAIT_TIMEOUT` bounds waiting for either a Conversation
writer or a global slot. The same Conversation remains single-writer even when
global capacity exists. Full queues return 429, expired waits 503, both with
`Retry-After`. Admission does not count calls or steps inside an admitted Run.
Multi Workers share that Run's admission slot, budget, and model limits; their
[isolation](execution-modes.md#isolated-worker-stage) is a Stage policy.

## 2. Model Request Limiter

- `MAX_CONCURRENT_MODEL_REQUESTS` counts physical Chat/Embedding HTTP requests,
  not models or pool connections. Streams hold a slot until the body closes.
- `MODEL_REQUESTS_PER_MINUTE` and `MODEL_TOKENS_PER_MINUTE` are per-key
  buckets; TPM estimates serialized input, not streamed output. Zero disables
  that bucket. Keyless requests still use global concurrency, not per-key buckets.
- Every retry needs a new permit and RPM/TPM reservation. Backoff holds no slot.
- Each attempt separates owner wait, RPM/TPM wait, global permit wait, and HTTP/stream time. These
  are local measurements, not provider queue/prefill time. Run admission wait
  is measured by RunController.
- The request timeout includes permit acquisition through body completion.
  Local expiry is `model_admission_timeout`, not retried; caller cancellation
  stays `canceled`. Owner wait has its own bounded admission inside this same
  Limiter, not a second Run scheduler.
- Requests above total TPM capacity fail with `request_token_capacity_exceeded`,
  not a Run Budget error.

### Owner-scoped Admission

`MAX_CONCURRENT_OWNER_MODEL_REQUESTS` (default 2) caps admitted physical attempts
for one verified user across **all** their Workspaces. The global default is 8;
startup requires a positive owner cap below the global cap, so global must be at
least 2. `OWNER_MODEL_QUEUE_SIZE` (default 8) permits additional pending attempts;
zero rejects excess immediately. `OWNER_MODEL_QUEUE_WAIT_TIMEOUT` (default 30s)
bounds owner-slot wait; the route/Embedding timeout also includes this phase.

Acquisition order is **owner reservation/slot -> API Key RPM/TPM -> global slot ->
HTTP body**. Owner-slot waiters never occupy global slots or consume key tokens.
Owner slots include downstream key/global wait and are held until body cleanup.
Cancellation, timeout, admission failure, EOF/body close, and limiter shutdown
release reservations exactly once; unused owner entries are removed. Existing
RPM/TPM reservations are not refunded if cancellation occurs after consumption.
Each physical retry reacquires capacity; provider backoff holds no slots.

Identity comes from the authenticated Session (`super` only in trusted-local
mode) or the persisted Run's immutable Workspace ownership.
Trusted-local HTTP requests bind `super` before dispatch, including Knowledge
ingestion/search and explicit Memory writes/recall that have no Run or Session.
Run attempts check current Membership and active Workspace both before waiting
and before transport;
missing/deleted/archived/revoked ownership fails closed. Resume passes existing
resource authorization first. Header/query/body/Tool values cannot choose a quota
owner. Single, Multi-Agent, Autonomous, compaction, title generation, Memory
extraction/sync, and Chat/Embedding requests share the same limiter; detached
execution retains identity and Memory workers resolve it from the durable Run.
Already-started provider requests are not retroactively canceled by revocation.

| Failure | JSON response | Retry behavior |
| --- | --- | --- |
| `owner_model_queue_full` | 429 + `Retry-After: 1` | New caller operation may retry; no automatic provider/Memory retry |
| `owner_model_queue_timeout`, `model_limiter_closed`, `model_owner_resolution_failed` | 503 + `Retry-After: 1` | Same local-overload boundary, not a provider fault |
| `model_owner_required`, `model_owner_unavailable` | 403 | Fix identity/access; no public-owner fallback |

An already-open Chat SSE remains HTTP 200: its typed `error` frame carries
`code`, `retryable`, and optional `retry_after_ms`; its HTTP status cannot change
after streaming starts. Attempt events persist `owner_capacity_wait_ms` separately
from key/global/HTTP timing, and Run Replay displays **Owner capacity wait**.

This is a **single-process capacity bound**, not strict fairness, a per-user
monthly token budget, a Workspace quota, or multi-instance accounting. Shared
key buckets and Run admission can still delay other owners; the cap does not
promise starvation-free scheduling. No Redis, virtual keys, or second scheduler
is introduced. See the focused [functional gate](../operations/functional-regression-testing.md#owner-model-admission).

The frozen [route catalog](model-routing.md) filters capability, input, and
output requirements before these permits. No eligible target returns
`model_route_unavailable` without HTTP work; priority and stable ID decide
among eligible targets.

## 3. Provider Timeout and Retry

Chat timeout comes from each route's `request_timeout_seconds`; Embedding uses
`EMBEDDING_REQUEST_TIMEOUT`. `MODEL_RETRY_MAX_ATTEMPTS` includes the first
attempt (1 disables retry); base/max delay bound exponential backoff and the
provider's `Retry-After`.

Transport failures, timeouts, rate limits, invalid provider responses, and 5xx
are retryable. Authentication, quota, missing model, invalid request, context
length, content policy, local token capacity, and cancellation fail immediately.
Provider 429/503 are distinct from local permit pressure. Stream retry stops
once a delta has been emitted, preventing duplicate output. Physical attempts
consume RPM/TPM but share one logical Run reservation.

## 4. Run Budget and Usage Ledger

`RUN_MAX_*` limits are frozen for one Run; zero disables the named dimension.

| Dimension | Accounting |
| --- | --- |
| Model calls | Logical operations; retries do not add calls |
| Prompt tokens | Estimated reservation followed by provider settlement |
| Completion tokens | Provider usage; remaining capacity also caps per-request output |
| Total tokens | Cumulative prompt + completion |
| Tool calls | Admitted valid calls; Handler failures/timeouts count |
| Runtime | Accumulated running segments; queue and human wait excluded |
| Estimated cost | Frozen input/output prices in integer microdollars |

Cost becomes enforced only with a positive `RUN_MAX_ESTIMATED_COST_USD` and
configured prices. The [Usage Ledger](run-budget.md) is accounting authority;
Trace/Episode are observational. That document owns reservations, settlement,
purpose scope, and observed-overage details.

## 5. Context Assembly and Compaction

`input capacity = context window - output reserve - safety margin`.
History, Memory, Knowledge, retrieved session sources, and Tool-result caps
apply to one input, not accumulated usage. Required protocol cannot be silently
dropped. Manifests explain selection, transformation, and estimates.

Skill metadata/active instructions use the same total capacity; resource reads
use the Tool-result/Artifact boundary. Package byte/count limits are independent
Loader/Snapshot bounds. Provider output uses the stricter of effective output
capacity and remaining Run completion budget.

Soft compaction is asynchronous after completion; hard compaction is preflight
work recorded with `compaction` ledger purpose. Selection, triggers, incremental
summary lineage, ratios, and failure guards belong to
[Context management](../context/context-management.md).

## 6. Autonomous Loop Guards

`AUTONOMOUS_MAX_ITERATIONS` and `AUTONOMOUS_MAX_OUTPUT_CHARS` are mode-owned.
An iteration normally has Observe/Plan/Act/Review/Decide, not one model request.
`AUTONOMOUS_MAX_RUNTIME_SECONDS` and `AUTONOMOUS_MAX_TOOL_CALLS` are folded
once into the stricter frozen Run Budget; only Budget enforces those resources.
Defaults produce effective 5m/20 caps versus general 15m/50. Tool Progress Guard
independently detects repeated work before Tool Budget is consumed.

## 7. Tool Execution Policy

Before a multi-round Tool batch, the caller needs an enforced logical-model-call,
prompt-token, or total-token limit, or an enclosing context deadline. A Tool-only
cap is insufficient because invalid arguments are not charged; a configured
runtime value alone is not an active deadline. No separate round counter is
introduced. See [Tool loop](../tools/bounded-tool-loop.md).

| Boundary | Default / fixed limit | Owning contract |
| --- | --- | --- |
| Tool timeout / result | 30s / 20,000 bytes | Live Binding; may override defaults |
| Schema / arguments | 65,536 bytes each | Catalog / Executor |
| Batch result / Artifact / preview | 8,000 bytes / 5 MiB / 1,000 bytes | [Result Artifacts](../tools/tool-result-artifacts.md) |
| Batch concurrency | 4 workers; serial unless declared safe read-only/keyed | Executor + live Binding |

JSON Schema 2020-12 normalization/compilation is shared by model definitions,
validation, frozen revision, and offline evaluation. Unsupported drafts, remote
references, non-object roots, and invalid/oversized schemas fail registration.
Arguments are bounded, decoded/canonicalized, schema-checked, and authorized
before Budget/Handler work. Rejections retain safe typed codes and JSON Pointer
paths without creating effects; argument hash and frozen revision also identify
journal/tracing records. See [Tool security](../tools/tool-security-policy.md)
and [contract/fault testing](../tools/tool-contract-testing.md).

Definitions, capability, operator policy, and progress settings are frozen;
Handler, timeout, result limits, and concurrency stay live. Large results use
redacted immutable Artifacts and bounded UTF-8 previews; source-ordered batch
accounting includes both raw results and previews.
`Security.SideEffect` alone derives the journal boundary: no writes need no
journal, internal writes use internal receipts, external/destructive writes use
external reconciliation. Committed results replay; uncertain writes require
[reconciliation](../tools/tool-side-effect-reconciliation.md), not blind retry.
One Handler timeout and cumulative Run runtime protect different resources.

## 8. Verification

Only an initial `completion_contract` enables Verification. Defaults/ranges:
30s per verifier (1ms-5m), two policy attempts (1-5), eight Artifacts per Evidence,
and 65,536 bytes per persisted Artifact/structured details.

These attempts are not provider retries. Built-in checks do not generate LLM
answers; `answer_relevance` does make two embedding requests per attempt under
shared request controls, not generation-token Ledger accounting.
See [Verification](verification.md) for config, blocking, and gate semantics.

## 9. Recovery Stale Threshold

`RECOVERY_STALE_RUN_TIMEOUT` (60s default) repairs stale open lifecycles at
startup before marking a Run `failed_recoverable`. Resume validates checkpoints
and the current supported Snapshot/Tool definitions. This threshold is neither
an operation timeout nor a Run runtime budget. See [recovery](durable-recovery.md).

## Frozen Protocol vs. Live Policy

| Frozen with Run | Live process / Binding policy |
| --- | --- |
| Run Budget, Context/Compaction, Loop limits | Admission, queues, permits, RPM/TPM |
| Agent(s), routes/model/endpoint, generation profiles, embedding identity | Credentials, retry/backoff, request timeouts |
| Tool definitions/capability/security/progress and Skill content | Handler, timeout/result/concurrency, runtime prerequisites |
| Completion Contract | Verifier deployment allowlists, capture policy, recovery threshold |

Only the current Snapshot schema is resumable; older records remain Replay-only.
The version and compatibility rule are owned by
[Runtime Snapshot](../architecture/terms.md#runtime-snapshot), not copied per feature.

## Tuning Order

1. Match Context capacity to the real model.
2. Set provider RPM/TPM and model permits, then Run admission/queue capacity.
3. Set cumulative Run cost/failure radius and mode iteration/output bounds.
4. Tune Tool, Verification, and Recovery timeouts separately.
5. Change one layer at a time; inspect Ledger, events, and Replay.

Use [bounded load evidence](../evaluation/load-soak-testing.md) after changing
controls. Never equate RPM with logical calls, TPM with accumulated usage,
iterations with calls, or Trace with Ledger.

## Checklist for a New Control

State scope, unit, single owner, enforcement point, frozen/live policy,
zero/negative/absent semantics, typed error/HTTP/SSE evidence, and the test that
prevents double counting. Update this map, `.env.example`, Config comments, and
boundary tests together. If an existing owner already limits that resource,
resolve policy precedence instead of adding another counter.
