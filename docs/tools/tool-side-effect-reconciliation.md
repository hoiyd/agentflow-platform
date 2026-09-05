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
available from the installed Binding. Omitting filters returns every effect for
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
compare that version, update the effect, and append one typed audit event. A
stale version returns `409`; a repeated `command_id` returns the current outcome
with `applied=false` and does not repeat a completed command.

Successful commands emit `tool.effect.reconciled`. A Tool-specific retry or
compensation failure emits `tool.effect.reconciliation_failed`, increments the
version, and leaves the effect in `needs_reconciliation` for a new operator
decision. Audit payloads include command, actor, reason, action, versions,
outcome, and final status, but exclude Tool arguments, provider result bodies,
and credentials.

Retry callbacks receive the original idempotency key. Compensation callbacks
receive a deterministic compensation key derived from the effect and command.
These hooks must honor those keys because no database transaction can make an
external API call atomic with local settlement. Callback availability is part
of the Tool Descriptor definition revision; a changed or legacy revision cannot
gain recovery authority retroactively.

The API has no built-in identity system. As with the rest of the current API,
deployments must protect this operator surface behind a trusted authentication
and authorization boundary. The `actor` field is audit attribution, not proof
of identity.

## State Model

```text
prepared -> executing -> committed
                      -> needs_reconciliation

needs_reconciliation -> committed    (confirm_committed or successful retry)
                     -> failed       (confirm_failed)
                     -> compensated  (successful compensation)
                     -> needs_reconciliation + version increment
                        (failed retry or compensation)
```

There is deliberately no universal compensator. Each external Tool owns the
provider-specific query, retry, or rollback behavior it can actually guarantee.
