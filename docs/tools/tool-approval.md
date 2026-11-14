# Durable Tool Approval

An explicit operator `ask` rule for **`file_patch`** pauses the original Run
before its Handler executes. Chat shows the target, exact replacement and expiry;
the owner can approve once, reject, revoke an unconsumed approval, or Stop the Run.
Replay retains the decision and links back to Chat. Refresh/restart does not lose
the pending call. Plan approval and clarification answers never grant Tool access.

## Enable

Keep the existing filesystem root, Workspace grant and Agent binding. In the
operator Tool configuration, change the **existing `file_patch` rule's action**
from `allow_and_log` to `ask`, retaining its exact capability declaration.
Do not copy a capability with different reversibility or widen the root map.
No default permission or environment variable changes are required.

Phase A only covers this explicitly configured managed-report consumer.
`deny` and `human_only` remain blocked; other Bindings, default `ask` without
an explicit rule, batch approvals and edited-argument approvals are not enabled.
A batch containing a `file_patch` that requires approval is rejected before any
call in that batch executes. A corrected call needs its own approval.
Direct human Undo/reconciliation retains its own authenticated command and receipt
checks; it does not create another model-call approval. `deny` and `human_only`
are not overridden by this exception.

## Contract

- Approval binds owner, Workspace, Run, real Stage (when present), logical Turn,
  call ID, canonical arguments/hash, definition revision, Policy hash, Snapshot
  hash and a **30-minute expiry**. Single keeps its real Turn; no Stage is invented.
- `tool_approvals` stores metadata and an opaque bounded protocol checkpoint.
  Checkpoints and execution-claim tokens never enter API responses or ordinary
  events. The startup schema creates the new table/index idempotently; existing
  records are not rewritten.
- Pending consumes no Handler execution or model permit. Waiting time does not
  count as active Run time. Resumption uses the same Run/Snapshot, original Tool
  arguments and provider continuation, including required `reasoning_content`;
  accumulated budgets are not reset.
- Resume rechecks current Agent/Workspace grants, schema, definition, policy,
  frozen context and budget. Any Policy hash change invalidates the approval
  conservatively, even if it appears unrelated to this call.
  A Run frozen under `allow` cannot execute through a new live `ask` rule;
  start a new Run to capture the approval-capable protocol.
- A transaction claims one execution epoch. Immediately before Effect Journal
  preparation, another transaction rechecks expiry, the approval's immutable
  binding, Run status and epoch, then consumes that claim. Concurrent/duplicate
  Resume cannot enter the Handler twice; an old worker cannot consume a new claim.
- `claimed`/`consumed` describe authorization, **not successful side effects**.
  Consult the Effect receipt. A committed receipt can replay without rewriting;
  a completed Turn/Stage reuses its output. Unknown publication/settlement stays
  behind existing reconciliation. This does not promise external exactly-once.

The trusted local configuration is re-read before the execution claim. This is
not a distributed policy transaction or protection against an operator who can
rewrite the database/process. Stop or revocation that wins before the claim
prevents execution; once claimed, cancellation can leave an uncertain effect.

## API and Evidence

| Endpoint | Purpose |
| --- | --- |
| `GET /api/runs/{id}/tool-approvals` | Authorized metadata/history; derived expired/canceled state |
| `POST .../{approval_id}/decision` | `{version, decision: approve/reject/revoke, idempotency_key}` |
| `POST .../{approval_id}/resume` | Admit the exact continuation; SSE after acceptance |

Stale, expired, conflicting or unauthorized decisions cannot grant execution.
Ordinary Continue/Resume cannot bypass an unresolved approval. Identical decision
retries are idempotent; approving does not itself execute the call. Rejection and
revocation cancel the waiting Run. Expiry prevents execution but does not run a
background cancellation job; use Stop to close the waiting Run.
Bound Resume also rejects expired approvals after a crash; it does not silently
renew consent. Historical decisions, output and Effect receipts remain readable.

Production integration scenarios exercise real HTTP, filesystem Bindings and
Postgres, with a deterministic provider and explicit crash-window seeding:

```bash
# From apps/api; TEST_DATABASE_URL must be a disposable test database.
go test ./app -run '^TestProductionDurableToolApproval$' -count=1

# From the repository root; screenshots and JSON evidence are test artifacts.
AGENTFLOW_TOOL_APPROVAL_TEST=1 AGENTFLOW_FILESYSTEM_WRITE_TEST=1 \
  bash scripts/test-browser.sh tool-approval.spec.ts
```

No new CLI approval command is included; headless consumers can use the same
authorized endpoints. Parameter editing and other Binding consumers remain Phase B.
