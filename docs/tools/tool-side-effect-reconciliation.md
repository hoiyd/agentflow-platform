# Tool Side-Effect Reconciliation

AgentFlow fails closed when an external Tool may have changed a remote system
but its outcome cannot be proved. The Tool Effect Journal records that window
as `needs_reconciliation`; it never treats a timeout as proof of failure and
never automatically replays the write.

Cold-start stale-Run repair also moves abandoned `executing` effects to
`needs_reconciliation` in the same transaction as the Run repair, so a worker
crash cannot leave an external write permanently hidden from this surface.

## Operator Surface

The Workspace-scoped internal API exposes the journal without returning stored
result bodies:

```text
GET  /api/runs/{run_id}/tool-effects?tool={name}&status={status}
POST /api/runs/{run_id}/tool-effects/{idempotency_key}/reconcile
```

The list response includes the effect version, frozen Tool definition revision,
status, request hash, timestamps, `has_result`, and the actions currently
available from the enabled Binding and current Tool security policy. Omitting filters returns every effect for
the Run.

A command has this shape:

```json
{
  "command_id": "incident-431-confirmation-1",
  "action": "confirm_failed",
  "expected_version": 2,
  "actor": "operator@example.com",
  "reason": "Provider audit log contains no matching request"
}
```

`confirm_committed` additionally requires a bounded JSON `result` that can be
returned by a later idempotent replay. Supported actions are:

| Action | Meaning | Required capability |
| --- | --- | --- |
| `confirm_committed` | The operator verified that the remote effect completed. | Operator evidence and a replay result. |
| `confirm_failed` | The operator verified that the remote effect did not occur. | Operator evidence. |
| `retry_with_same_key` | Invoke the Binding's dedicated idempotent retry hook. | Frozen `retry_with_same_key` declaration and current matching callback. |
| `compensate` | Invoke the Binding's dedicated compensation hook. | Frozen `compensate` declaration, current matching callback, and non-irreversible security policy. |

## Consistency And Audit

Every command carries an `expected_version`. FileStore and Postgres atomically
compare that version, update the effect, and append a typed audit event.
Manual confirmations use one commit. External callbacks first commit a
`reconciling` claim and `tool.effect.reconciliation_started`, then execute outside
the storage transaction, then commit their outcome against the claimed version.
Competing commands cannot both enter the callback, even when both read the same
initial version. A normal callback attempt advances the version twice.

A stale version or changed payload under the same `command_id` returns `409`.
The command hash binds the normalized request, including its original expected
version and result. Repeating the same command returns its recorded outcome with
`applied=false`; a claim without settlement returns `outcome=pending`. This means
an unresolved claim, not a guarantee that a worker is currently running.
Effects in the response reflect the current journal state, which may include a
later manual decision. Historical commands without a verifiable command hash
return a conflict rather than silently authorizing another execution.

Successful commands emit `tool.effect.reconciled`. A Tool-specific retry or
compensation failure emits `tool.effect.reconciliation_failed`, increments the
version, and normally leaves the effect in `needs_reconciliation` for a new
operator decision. Deadline/cancellation instead retain `reconciling`, because
the callback may still be executing. A crash or failed settlement also leaves
the durable claim intact. No background process reclaims or retries it.
Audit payloads include command hash, actor, reason, action, versions,
outcome, and final status, but exclude Tool arguments, provider result bodies,
and credentials.

Retry callbacks receive the original idempotency key. Compensation callbacks
receive a deterministic compensation key derived only from the effect. A new
operator command ID therefore cannot cause a second logical compensation.
These hooks must honor those keys because no database transaction can make an
external API call atomic with local settlement. Callback availability is part
of the Tool Descriptor definition revision; a changed or legacy revision cannot
gain recovery authority retroactively.

## Execution Boundary

Callbacks must be enabled, match the recorded definition revision, and pass
`toolpolicy.Evaluate` using their full declared scope and the current operator
policy. A missing rule, `ask`/`human_only`, revoked enablement, or unavailable
credential scope fails closed before a claim. There is no credential resolver
on this path yet; declaring a credential scope does not supply a credential.
These are explicit operator operations, not model Tool calls, so they do not
consume a new model Run Budget reservation or reuse a model-supplied grant.

The callback deadline is the smaller of the request deadline and Binding
timeout, capped at the shared Tool default of 30 seconds. A buffered completion
channel bounds caller waiting even if trusted Binding code ignores cancellation.
Go cannot kill that goroutine: Binding implementations must propagate context
to their I/O, and untrusted code still needs a real process sandbox.
Late results never mutate the journal from the background goroutine.

Only `confirm_committed` and `confirm_failed` remain available for a `reconciling`
effect. Before confirming, operators must quiesce any surviving executor and
verify the provider's authoritative state; a local timeout is not evidence of
failure. A confirmation invalidates the older claim version, so a late local
settlement cannot overwrite it. Failed manual confirmation cannot release a claim.
This is fail-closed recovery, not exactly-once execution of arbitrary providers.

Result envelopes are validated and size-limited, then passed through the existing
deterministic JSON redactor before persistence. Actor/reason/error text is
redacted before truncation and audit storage. Known credential shapes are
removed; this is not a guarantee of detecting arbitrary sensitive business data.
The same rules apply to manually supplied results and callback errors/panics.
The Binding's smaller result limit also applies to callback replay envelopes.
Command IDs containing recognized credential patterns are rejected, not persisted.

The API has no built-in identity system. As with the rest of the current API,
deployments must protect this operator surface behind a trusted authentication
and authorization boundary. The `actor` field is audit attribution, not proof
of identity.

## State Model

```text
prepared -> executing -> committed
                      -> needs_reconciliation

needs_reconciliation -> committed    (confirm_committed)
                     -> failed       (confirm_failed)
                     -> reconciling  (durable callback claim)

reconciling -> committed / compensated  (callback settlement)
            -> needs_reconciliation    (callback returned an error)
            -> reconciling             (deadline/cancel; crash keeps the claim)
            -> committed / failed      (explicit operator confirmation)
```

Committed, failed, and compensated effects cannot be reopened by a late normal
execution failure. Cold-start stale-Run repair touches abandoned `executing`
effects only, not reconciliation claims. Existing text status and JSON event
columns store the added state/event without a schema migration or cleanup job.
FileStore remains single-process; shared multi-process storage requires Postgres.
Replay's generic event and effect records accept them without a new frontend
enum; the list API also accepts `status=reconciling`.

There is deliberately no universal compensator. Each external Tool owns the
provider-specific query, retry, or rollback behavior it can actually guarantee.
