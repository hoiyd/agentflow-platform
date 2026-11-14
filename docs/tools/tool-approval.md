# Durable Tool Approval

An operator `approval_required` policy pauses a registered Tool call before its
Handler executes. Chat shows the Tool, complete arguments and expiry;
the owner can approve once, reject, revoke an unconsumed approval, or Stop the Run.
Replay retains the decision and links back to Chat. Refresh/restart does not lose
the pending call. Plan approval and clarification answers never grant Tool access.

## Enable

In the operator Tool configuration, set the relevant rule's `action` to
`approval_required`, retaining its exact capability declaration. For example,
`file_write` and `file_patch` use the same approval flow as a calculator or an
installed MCP Binding. There is no Tool-name allowlist or second approval switch.
`default_action` also applies to local non-sensitive Tools; sensitive and remote
Tools still require explicit rules. Defaults and existing local files are unchanged.

The Tool must already be installed, ready, enabled in the Workspace, and available
to the Agent and execution mode. Approval cannot add capabilities: an isolated
Multi-Agent Worker still cannot mutate parent Task State or request parent input.
Runtime-only discovery retains its own bootstrap policy, not a new operator
configuration surface in this change. `deny` and `human_only` remain blocked.

Calls needing approval must be sent alone; a mixed batch is rejected before any
call executes. A corrected call needs fresh approval. Parameter editing and batch
approvals are not supported. Direct human Undo/reconciliation uses its own
authenticated command and exact receipt/version checks, not another model-call
approval; deny, human_only and scope/credential restrictions still apply.

New policy actions and capability approval modes use `approval_required`. The old
`ask` spelling is still read with the same meaning; stored configurations and frozen
Snapshots retain their original values/hashes. Explicitly renaming an existing rule
changes its Policy hash and invalidates pending approval, as any policy edit does.

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
  A Run frozen under `allow` cannot execute through a new live `approval_required` rule;
  start a new Run to capture the approval-capable protocol.
- A transaction claims one execution epoch. Immediately before Effect Journal
  preparation, another transaction rechecks expiry, the approval's immutable
  binding, Run status and epoch, then consumes that claim. Concurrent/duplicate
  Resume cannot enter the Handler twice; an old worker cannot consume a new claim.
- `claimed`/`consumed` describe authorization, **not successful side effects**.
  Consult the Effect receipt. A committed receipt can replay without rewriting;
  a completed Turn/Stage reuses its output. Unknown publication/settlement stays
  behind existing reconciliation. This does not promise external exactly-once.
- Read-only calls have no write receipt and may be recomputed after a crash.
  Approved results pass through normal Tool observation handling, including
  source labeling and control interrupts; approving a question Tool does not
  answer its questions.

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

Production integration scenarios exercise real HTTP, filesystem/calculator/Task
State/clarification/MCP Bindings and Postgres, with a deterministic provider and
explicit crash-window seeding:

```bash
# From apps/api; TEST_DATABASE_URL must be a disposable test database.
go test ./app -run '^TestProductionDurableToolApproval$' -count=1

# From the repository root; screenshots and JSON evidence are test artifacts.
AGENTFLOW_TOOL_APPROVAL_TEST=1 AGENTFLOW_FILESYSTEM_WRITE_TEST=1 \
  bash scripts/test-browser.sh tool-approval.spec.ts
```

No new CLI approval command is included; headless consumers can use the same
authorized endpoints. Parameter editing and batch approval remain conditional follow-ups.
