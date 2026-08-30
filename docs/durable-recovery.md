# Durable Recovery and Stage Checkpoints

AgentFlow treats process interruption as a protocol-recovery problem, not only
as a Run status update. The implementation has three cooperating parts:

1. cold-start lifecycle repair;
2. durable Stage checkpoints;
3. an idempotency journal for tools with external side effects.

These records use the same File/Postgres persistence boundary and are visible
through Run Replay. They contain hashes and execution metadata, never API keys.

## Cold-Start Lifecycle Repair

At startup, `recovery.MarkStaleRunningRuns` scans only Runs whose status is
`running` and whose heartbeat is older than `RECOVERY_STALE_RUN_TIMEOUT`.
Active Runs owned by the current process are not scanned.

For each stale Run, the event protocol finds open scopes and appends synthetic
terminal events in inner-to-outer order:

```text
tool.failed -> model.failed -> turn.failed -> stage.failed
```

Every synthetic payload includes:

```json
{
  "synthetic": true,
  "reason": "worker_interrupted",
  "repaired_from_event_id": "..."
}
```

The store rechecks the stale heartbeat and expected event cursor before making
changes. Appending terminal events, changing running Stage records to `failed`,
and changing the Run to `failed_recoverable` are one File lock/save or one
Postgres transaction. A second startup scan observes a non-running Run and does
nothing, so repair is idempotent.

Recovery planning or persistence failure prevents API startup. It does not
silently leave an inconsistent Run active.

Historical event streams created before strict Stage pairing may contain a
terminal Stage event without a start event. They remain readable. New streams
that contain checkpoint events use strict Stage start/terminal validation.

## Stage Checkpoint Protocol

`checkpoint.InternalProvider` records one mutable checkpoint per
`run_id + stage_id`. Its immutable identity contains:

- input hash;
- Runtime Snapshot hash;
- frozen Tool definition hash;
- provider name (`internal_state_v1`).

Its progress fields contain output hash, event cursor, error, and status:

| Status | Meaning |
| --- | --- |
| `prepared` | Internal state was recorded before `stage.started`. |
| `executing` | `stage.started` is durable and work may be in flight. |
| `committed` | `stage.completed` and its output hash are durable. |
| `needs_reconciliation` | The Stage ended or was interrupted without a safe commit. |
| `compensated` | Incomplete internal Stage state was explicitly abandoned during Resume. |

Stage start writes `prepared`, persists `stage.started`, then advances to
`executing`. Stage completion persists `stage.completed` before advancing to
`committed`. A crash between these writes therefore leaves a conservative,
detectable state; Resume never assumes an uncertain Stage committed.

Resume recalculates the Runtime Snapshot and Tool definition hashes. A mismatch
emits `checkpoint.stale` and fails closed. A committed Stage emits
`checkpoint.restored` and is not rerun. An interrupted Stage with no uncertain
external effect is marked `compensated`; an uncertain effect blocks Resume with
a reconciliation error.

Resume compatibility is intentionally bounded to the current Runtime Snapshot
schema and its immediately preceding version (currently v9 and v8). Older Runs
remain readable through Replay, but attempting to Resume them returns
`runtime_snapshot_resume_unsupported`. This prevents old execution protocols
from silently inheriting current model, Tool, context, or budget behavior.

## Tool Side-Effect Idempotency

Tool descriptors default to `side_effect.mode=none`. A write-capable Binding
must explicitly declare `external`. Before such a handler runs, Tool Executor
requires Run, Stage, and Tool Call identity plus a durable Tool Effect Journal.
It derives a stable idempotency key when the caller does not provide one.

The journal uses these states:

```text
prepared -> executing -> committed
                      -> needs_reconciliation
needs_reconciliation -> compensated (future business-specific compensator)
```

- A new key enters `executing` before handler invocation.
- A successful bounded result is stored as `committed`.
- Repeating a committed request returns the stored result with `replayed=true`
  and does not invoke the handler.
- An existing `executing` or `needs_reconciliation` request fails closed; it is
  never retried as if nothing happened.
- Timeout, cancellation, panic, result-encoding failure, or loss of the journal
  commit marks the effect `needs_reconciliation` because an external write may
  already have occurred.

Replay exposes Tool Effect status, request hash, and `has_result`, but not the
stored result body. Tool result content remains governed by existing Tool trace
and result-size policy.

## Shutdown Ordering

`RunController.CloseAndWait` rejects new reservations and waits for all accepted
reservations to complete or cancel. Application shutdown drains accepted Runs
and Memory Curation before closing persistence. If Run drain times out, the
Store is not closed underneath active work and shutdown returns an error.

## Current Boundary

- FileStore and Postgres implement the same repair, checkpoint, and Tool Effect
  contracts.
- Automatic `failed_recoverable` execution Resume currently supports
  Autonomous Runs. Other modes remain replayable and repairable but do not
  silently restart from an unsupported Stage.
- The checkpoint provider captures AgentFlow internal state. Business-specific
  external compensation handlers are not implemented.
- Multi-instance Worker takeover still requires Lease/Heartbeat/Fencing or a
  Postgres ownership lock. This implementation provides single-process durable
  recovery, not distributed exactly-once execution.
