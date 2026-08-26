# Event Projections and Runtime Invariants

AgentFlow treats persisted Run Events as execution facts and derives read
models from them. This layer does not introduce event-sourcing infrastructure,
projection tables, or a second source of truth.

## Event protocol

Every Run Event is registered in the executable
[`eventcatalog`](../apps/api/internal/eventcatalog) with:

- durability: `durable` or `live`;
- schema version and legal Run/Stage/Turn scope;
- payload family;
- lifecycle role and terminal pairing;
- known read-side consumers.

`model.delta` and `run.progress` are live-only. File Store and Postgres reject
them at the persistence boundary, so Replay contains facts rather than UI
progress. Unknown event types and unsupported schemas are rejected before a
durable write. The generated [Run Event Catalog](event-catalog.md) is checked
against the Go declarations in tests; adding an event without registering and
documenting it fails CI.

## Canonical projections

`projection.BuildSnapshot` builds three pure read models from one durable event
watermark:

| Projection | Source | Purpose |
| --- | --- | --- |
| Run | Run record + Run Events | status, active Stage/Turn/Model/Tool scopes, trace totals |
| Usage | Usage Ledger | model/tool calls, tokens, estimated cost, reservations |
| Verification | Run + immutable Evidence | current status, subject, attempt, fresh evidence count |

Each projection and the combined snapshot expose `as_of_sequence`. File Store,
Postgres, Replay, and `GET /api/runs/{id}/projection` call the same reducers.
There is no persisted projection cache; rebuild cost and real profiling data
must justify one later.

Replay keeps the older top-level `summary` and `usage_ledger` fields for API
compatibility, but both are now populated from the canonical projection.

## Runtime invariants

The lightweight invariant registry runs package-owned checks and reports
stable diagnostics without making historical Replay unreadable. A failure has
`code`, `owner`, `run_id`, optional `event_id` and `sequence`, and a human
message.

The initial checks cover:

- continuous sequence and Stage/Turn/Model/Tool lifecycle pairing;
- Model Request reconstruction against Runtime Snapshot, Context Manifest, and
  prepared-event hashes;
- idempotent Usage settlement and reservation pairing;
- Verification Evidence freshness for the latest subject.

Execution/recovery code can still use hard gates such as `ValidateLifecycle`.
The Replay and projection APIs use diagnostic mode so a corrupt historical Run
can be inspected and repaired. `RUNTIME_INVARIANT_MODE=report` is the default:
it logs and returns isolated diagnostics without changing the Run result. Local
or CI environments can use `fail` to make projection reads fail loud.

## Snapshot and live handoff

The in-memory event Hub serializes snapshot loading and subscription
registration per Run. A durable event committed during that handoff is either
included at the snapshot watermark or buffered after it, never lost between
the two. Durable events are published only after Store commit succeeds.

Subscriber backpressure never blocks execution. A subscriber that exhausts
its bounded buffer receives typed `event_subscriber_lagged`, disconnects, and
reconnects from its last durable sequence. Durable notifications are buffered
by sequence, so concurrent post-commit publication cannot reorder them.
Live-only events have no replay guarantee and are intentionally absent from the
durable cursor.

This Hub is process-local. Multi-instance fan-out remains deferred; durable
reconnection continues to rely on Store sequence cursors rather than an
in-memory delivery guarantee.
