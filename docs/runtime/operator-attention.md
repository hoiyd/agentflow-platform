# Operator Attention Projection

AgentFlow derives an operator queue from existing durable Run records. It does
not persist an `attention_status`, duplicate Run state, or introduce another
recovery command path.

`GET /api/runs/attention` returns at most one item per actionable Run. Each item
contains the reason, current Run status, Recovery Evidence, one recommended
Recovery Action, and the highest durable Run Event sequence observed while the
item was built. Healthy completed, active, queued, and canceled Runs are
omitted; unresolved external Tool Effects still surface regardless of status.

## Derivation

The projection reuses the same Recovery Summary shown by Run Replay. Within one
Run, uncertain Tool Effects take precedence, followed by Verification failures,
structured Task State blockers, exhausted Run Budget, and terminal Run status.
The queue is then ordered by operator urgency:

1. `reconciliation_required`
2. `recovery_available`
3. `waiting_for_user`
4. `verification_attention`
5. `budget_exhausted`
6. `failure`

Ties are ordered by the oldest Run update and then Run ID. Missing Evidence does
not hide an item. When the existing action cannot run, the response retains the
disabled action and its `unavailable_reason`.

`observation_sequence` is the highest sequence in the durable Run Events used
for the projection. Clients should refresh after taking action; it is a read
watermark, not a lock or mutation precondition.

## Frontend

The workbench exposes the queue as a separate **Needs attention** view. It loads
only when opened, keeps the last confirmed result visible during refresh, and
links each item to the existing Run Replay recovery controls. Recent Runs and
normal Replay remain unchanged for healthy work.
