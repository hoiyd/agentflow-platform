# Isolated Worker Stage

Multi mode uses one persisted Run with five ordered Stages:

```text
Planner -> Router -> Worker -> Reviewer -> Finalizer
```

The Worker is isolated at the Stage boundary. It receives only the approved
task, the Router-selected frozen Agent, that Agent's frozen Tool allowlist, and
explicitly retrieved Memory/Knowledge. Conversation history, Context
Compaction, durable Task State injection, and the `update_task_state` Tool are
excluded from the Worker model request.

This boundary does not create a Child Run. The Worker Stage uses the parent
Run's frozen model routing, Run Budget, Usage Ledger, Tool Effect records,
Artifacts, events, and recovery protocol. This keeps one lifecycle authority
while preserving specialist isolation.

## Result Handoff

The complete Worker output is persisted on its `CollaborationStep`. Reviewer
and Finalizer receive at most 4,000 characters. When truncation is necessary,
the handoff includes a stable `run://<run_id>/stages/<stage_id>` reference to
the complete Stage output.

## Recovery

Startup recovery marks a stale parent Run `failed_recoverable` and closes any
open Stage lifecycle. Resume validates the frozen Runtime Snapshot and durable
checkpoints, then:

1. reuses completed Planner, Router, Worker, Reviewer, and Finalizer Stages;
2. reruns only the first missing or interrupted Stage;
3. refuses Resume while a Tool Effect needs reconciliation.

There is no parent/child ownership state to reconcile. Replay, usage, model
requests, Tool Effects, and Artifacts are queried by the single Run ID.

## Legacy Postgres Data

On upgrade, the schema migration copies a completed legacy Child Worker output
to its parent Worker Stage and reassigns its usage, model-request, Tool Effect,
Artifact, checkpoint, and non-lifecycle trace evidence to that Stage. It then
removes legacy delegation events and Child Run rows before dropping
`run_delegations`.

Legacy in-flight Runs remain replayable but are not resumed across the removed
parent/child protocol boundary; restart those tasks as a new Run. Runs created
after this migration recover directly from their parent Stage checkpoints.
