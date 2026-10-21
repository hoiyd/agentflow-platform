# Execution Controls and Resource Boundaries

Compare **scope, unit, and enforcement owner**, not similarly named settings.
This map explains control precedence. Defaults live in
[`.env.example`](../../apps/api/.env.example); setup lives in
[backend configuration](../operations/backend-configuration.md).

## Quick Diagnosis

| Symptom | Inspect |
| --- | --- |
| Too many active/waiting tasks | Run admission and Conversation writer |
| One owner's requests monopolize capacity | Owner slot/queue, then shared key/global limits |
| Provider 429 / too many HTTP calls | Physical-request limiter and provider retry |
| One Run costs too much | Cumulative Run Budget / Usage Ledger |
| Input does not fit | Context Assembly, not accumulated Run tokens |
| Repeated work | Loop / Tool Progress Guard, not provider retry |
| Tool hangs / spills / writes ambiguously | Executor, result caps, effect journal / reconciliation |
| A crashed Run stays open | Lifecycle repair and checkpoints |

## Ownership Matrix

| Control | Scope / unit | Enforcement and state |
| --- | --- | --- |
| Run admission | Process / Conversation; active and queued Runs | `RunController`; live process policy |
| Model limits | Process, owner, API key; physical requests and estimated input TPM | `ModelRequestLimiter`; live permits/buckets |
| Routing | Run; eligible target for one logical call | Frozen catalog / affinity |
| Retry | Model Call; physical attempts | Provider policy; live backoff |
| Run Budget | Run; logical calls, tokens, Tools, active time, estimated cost | Frozen limits; durable Ledger |
| Context | Model Call; input/output capacity | Frozen config; Manifest per assembly |
| Loop / progress | Run; iterations, output, repeated work | Frozen mode/Guard settings |
| Tool execution | Call/batch; authority, time, bytes, effects | Frozen definitions/policy, live Handler, durable receipts |
| Verification | Contracted Run; checks and attempts | Frozen contract, immutable Evidence; live deployment grants |
| Recovery | Startup/Resume; stale lifecycle and checkpoints | Durable state; live stale threshold |
| Observability | Events and derived views | Durable facts, not enforcement |

## 1. Run Admission and Conversation Concurrency

`MAX_CONCURRENT_RUNS` caps active Runs; `RUN_QUEUE_SIZE` is additional waiting
capacity. `RUN_QUEUE_WAIT_TIMEOUT` bounds waiting for a Conversation writer or
global slot. Full queues return 429; expired waits 503, both with `Retry-After`.
One Conversation remains single-writer even with spare global capacity. Multi
Workers are isolated Stages sharing their parent's slot, Budget and model limits.

## 2. Model Request Limiter

`MAX_CONCURRENT_MODEL_REQUESTS` counts physical Chat/Embedding HTTP attempts;
streaming holds its slot through body close. Per-key RPM/TPM estimate serialized
input, not generated output; zero disables the named bucket. Keyless requests
still use global concurrency. Retries reacquire permits and do not hold them
during backoff. Attempts exceeding total TPM capacity fail before transport.

### Owner-scoped Admission

One trusted owner shares a bounded slot/queue across all their Workspaces.
Acquisition order is **owner -> key RPM/TPM -> global -> HTTP/body**. Owner wait
does not consume global slots or key tokens; downstream waits do occupy the owner
reservation. Cleanup releases it once; consumed key reservations are not refunded.

Identity comes from Session/persisted Run ownership (`super` in trusted-local
mode), never request arguments. Membership/active Workspace are checked before
waiting and before transport; started requests are not retroactively canceled
by revocation. Detached work and auxiliary Chat/Embedding calls use this same
limiter. Owner cap must be positive and below global capacity.

| Failure | HTTP behavior |
| --- | --- |
| `owner_model_queue_full` | 429 + `Retry-After: 1` |
| `owner_model_queue_timeout`, `model_limiter_closed`, `model_owner_resolution_failed` | 503 + `Retry-After: 1` |
| `model_owner_required`, `model_owner_unavailable` | 403; no public-owner fallback |

Local admission expiry is not provider failure/retry. Open SSE keeps HTTP 200 and
sends typed error frames. Attempt evidence separates owner, key, global and HTTP
wait; those are client measurements, not backend queue/prefill. This is a
single-process capacity cap, not strict fairness, monthly quota or distributed
accounting. See [functional gate](../operations/functional-regression-testing.md#owner-model-admission).

The [route catalog](model-routing.md) rejects unmet capabilities/capacity before
HTTP; `model_route_unavailable` is not transient retry. Optional
[TEI reranking](../knowledge/cross-encoder-reranking.md) has separate process batch
slots/deadline, immediate excess rejection and unknown cost; it is not covered by
Chat/Embedding RPM/TPM, owner queue or Run generation-token accounting.

## 3. Provider Timeout and Retry

Route timeout / `EMBEDDING_REQUEST_TIMEOUT` includes permit acquisition through
body completion. `MODEL_RETRY_MAX_ATTEMPTS` includes the first attempt; bounded
backoff respects provider `Retry-After`.

Transient transport/timeouts/rate limits/invalid responses/5xx may retry.
Authentication, quota, missing model, validation, Context length, content policy,
local capacity and cancellation do not. Stream retry stops after the first emitted
delta. Physical attempts consume key capacity but share one logical Run reservation.

## 4. Run Budget and Usage Ledger

Frozen `RUN_MAX_*` limits bound logical model calls, cumulative prompt/completion/
total tokens, admitted valid Tool calls, active time and estimated cost. Zero
disables that dimension. Queue/human wait are not active runtime; Handler failure
still counts as admitted work. Retry does not add a logical model call.

Estimated input reservations settle against provider usage. Remaining completion
capacity caps output; cost enforcement needs configured frozen prices. The
[Ledger contract](run-budget.md), not Trace/Episode totals, owns exact settlement,
auxiliary purposes, estimates and observed overages.

## 5. Context Assembly and Compaction

One input uses `context window - output reserve - safety margin`. Source limits
and required protocol apply independently of cumulative usage. Skills share that
capacity; resource reads use Tool result/Artifact limits. Output uses the stricter
of route capacity and remaining Run completion allowance.

Soft compaction follows completion; hard compaction is Turn preflight, accounted
with `compaction` purpose. [Context management](../context/context-management.md)
owns selection, lineage, trigger/failure semantics and lossy-summary limits.

## 6. Autonomous Loop Guards

Iterations and output characters are mode-owned. Runtime/Tool-call settings
resolve once to the stricter frozen Run Budget instead of competing counters.
An iteration contains several Stages, not one request. Progress Guard detects
repetition before spending more Tool Budget.

## 7. Tool Execution Policy

Discovery controls visible schemas, not authority. Calls validate bounded JSON
arguments/schema, frozen revision and current grants before Budget/Handler work.
`Security.SideEffect` derives journal mode: no write, internal receipt, or external
reconciliation. Committed results replay; uncertain writes never retry blindly.

Definitions/security/progress freeze; Handler, timeout, result and safe concurrency
remain live. [Tool security](../tools/tool-security-policy.md),
[Artifact limits](../tools/tool-result-artifacts.md) and
[reconciliation](../tools/tool-side-effect-reconciliation.md) own exact rules.

Multi-round execution requires an enforced logical-call/token bound or enclosing
deadline. Tool-only limits cannot stop infinite invalid calls, which are not
charged. No independent round counter competes with existing owners.

`plan_only` freezes no-write authority separately from mode; subsequent execution
uses a new Run, not approval that widens the plan's authority.
[Sandbox commands](../operations/sandbox-execution.md) add explicit VM, resource,
output and cleanup limits; scratch isolation does not grant host mounts or Skill
script execution. Neither sandbox resource caps nor Tool timeouts replace Run Budget.

## 8. Verification

Only initial `completion_contract` enables checks. Attempts are not model retries;
deployment HTTP/command allowlists remain live. `answer_relevance` consumes
embedding requests under shared controls, not generation-token Ledger entries.
[Verification](verification.md) owns checks, Evidence and exhaustion policy;
[execution boundaries](../operations/execution-boundaries.md) owns destination/
host-process limits.

## 9. Recovery Stale Threshold

`RECOVERY_STALE_RUN_TIMEOUT` repairs stale lifecycles at startup before explicit
Resume. It is neither an operation timeout nor active-runtime Budget. Resume
validates current access, checkpoints and supported Snapshot/Tool revisions.
See [recovery](durable-recovery.md).

## Frozen Protocol vs. Live Policy

| Frozen with Run | Live |
| --- | --- |
| Agents, routes, sampling, embedding identity | Credentials, request controls, retry/backoff |
| Run Budget, Context/Compaction, Loop/progress settings | Current identity/grants, process admission |
| Tool definitions/security, Skills, Completion Contract | Handler bounds, verifier allowlists, Capture, recovery thresholds |

Only current Snapshot schemas are resumable; older records remain Replay-only.
[Snapshot compatibility](../architecture/terms.md#runtime-snapshot) is authoritative.

## Tuning and Extension

Match real model capacity first, then request/Run admission, then cumulative
Budget and subsystem timeouts. Change one layer at a time and inspect Ledger/
Events with [bounded load evidence](../evaluation/load-soak-testing.md).

A new control must state scope, unit, owner, enforcement point, frozen/live and
zero/absent semantics, failure evidence and a boundary regression. If an existing
owner limits the resource, resolve precedence there instead of adding a counter.
