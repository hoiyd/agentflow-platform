# Bounded Multi-Round Tool Loop

## Contract

One Turn may contain multiple model requests and guarded Tool batches:

```text
assemble context -> model response
  -> Tool calls -> execute -> paired observations -> assemble again
  -> no Tool calls -> final answer
```

Single, the isolated Multi Worker Stage, and Autonomous Act use the same loop.
The Autonomous iteration count is not a Tool-loop limit. Each request uses the
frozen Tool definitions and existing Context Assembler, Manifest, Request
Capture, Run Budget, Usage Ledger, and Progress Guard.

Tool-enabled rounds use native non-streaming completion so one response can
choose calls or the final answer. Only the final answer is forwarded as SSE
deltas; intermediate model commentary is not the persisted answer. No extra
answer-generation request is made. Tool-free and explicitly simulated Chat
retain their existing streaming paths. Do not interpret buffered answer delivery
as provider token streaming or measurable time to first token.

## Failure Inventory and Test Plan

| Failure | Required result |
| --- | --- |
| No effective model-call/token limit and no enclosing deadline | Refuse the Tool loop before executing a Binding; normal direct answers still work. |
| Valid JSON violates a Tool schema, missing Tool, empty result, read timeout | Return the existing typed observation; the model may correct it within the same budget. |
| Incomplete generation or malformed Tool-call protocol/JSON | Stop without executing any call from that batch. |
| Reused provider Call IDs across rounds | Independent ledger/event identities and correctly paired messages. |
| Duplicate provider Call IDs within one batch | Reject the batch before any side effect. |
| Model-call, token, Tool-call or active-runtime exhaustion | Existing typed budget failure; do not invent a successful final answer. |
| Repeated no-progress results | Existing Progress Guard eventually halts the Turn. |
| Cancellation, provider error or empty final answer | Stop with an error; never synthesize `Tool execution completed.`. |
| Required context exceeds the window | Persist the failed assembly diagnostic and stop before another model request. |
| Optional history exceeds its budget | Clip optional history without dropping or mismatching Tool call/result pairs. |
| Multiple Web searches each return local W1 | Relabel observations using the durable Run-scoped source catalog; do not collide. |
| Explicit same-Stage retry after a committed external effect | Reuse the existing Effect Journal; do not repeat the committed write or silently accept changed arguments. |
| Uncertain external effect or journal/audit failure | Stop for existing recovery/reconciliation; the model cannot simply ignore it. |

The deterministic backend integration tests use a local OpenAI-compatible HTTP
server, real Tool bindings, Context Assembly, an in-memory Fixture Store for
events/capture, and Usage Ledger. Their scope is the backend Runtime/Tool protocol,
not browser-to-API product end-to-end testing or real PostgreSQL integration.
It is protocol evidence, not a live-model quality benchmark. TOOL-025 owns live
task-quality evidence; this feature does not introduce another evaluator.

## Recovery Boundary

Tool identities are derived from Run, Stage, round and batch position, not from
a provider's possibly reused identifier. Stage retry reuses those identities;
the existing journal validates the original arguments before replaying a
committed result. Changed arguments cannot silently repeat a write. An unscoped
standalone caller receives invocation-local identities, not a durable recovery
guarantee.

Actual orchestration Resume may abandon an unfinished Stage and create a new
Stage ID. If that Stage already has a committed external effect, checkpoint
restore refuses automatic recreation with `ErrNeedsReconciliation`. An operator
must resolve the unfinished Stage/effect boundary; replaying a completed Stage
or an explicit retry of the same Stage identity is a different case. Confirming
an external write alone does not prove its enclosing Stage completed.

Recovery remains Stage-scoped. There is no durable per-round cursor, extra
checkpoint type, Child Run or exactly-once claim. A retry may repeat model calls
and read-only work; it must not automatically repeat a confirmed external write.

## Reproduce Protocol Evidence

From `apps/api`, using the repository Go version:

```bash
go test -json ./internal/agent ./internal/agent/toolloop ./internal/checkpoint ./internal/httpapi ./internal/projection \
  -run 'TestBoundedToolLoopAcrossExecutionModes|TestMultiRound|TestRestoreDoesNotAbandonCommittedEffect|TestResumeFailurePolicyKeepsCheckpointBlockedRunRecoverable|TestBuildRecoverySummaryExplainsCommittedEffectInUnfinishedStage' \
  -count=1 -timeout=60s > /tmp/bounded-tool-loop-evidence.jsonl
```

The JSONL retains test/subcase identity, Run/Stage IDs, fixture model and budget,
event lifecycle, result and pass/fail outcome. Test sources define exact inputs
and local provider responses. Tests verify settled logical calls against
physical Request Capture records and unique Tool identities. This uses no live
provider, credentials, production database or quality judge. The Mode test
executes actual Single, approved Multi Worker and Autonomous Act entry points;
the recovery test distinguishes same-Stage journal replay from cross-Stage
Resume refusal. No browser automation or visual claim is included.
