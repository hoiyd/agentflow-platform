# Read-only Planning

`execution_constraint=plan_only` is an opt-in Run restriction, independent of
Single, Multi-Agent and Bounded Loop orchestration. Omission preserves ordinary
execution. The frozen restriction can only reduce existing authorization.

Planning exposes authorized read Tools, but rejects mutations in the Executor,
including forged calls, Task State writes and sandbox commands. Run, Trace,
Usage, clarification and Context bookkeeping remain writable by the harness.
Skill text and model claims cannot grant permissions.
Classification trusts validated local `Descriptor.Security`, not an automatic
proof of arbitrary Handler purity. New Bindings still need contract/fault tests.

## Use and Persistence

Select **Read-only planning** beside Verification before sending a task, or add
`"execution_constraint":"plan_only"` to `POST /api/chat`. This is separate from
Multi-Agent's existing planner Stage/Continue approval, which does not grant
write access to a planning Run. Omit the field for ordinary execution.
The browser checks `/health.planning_contract_version` before planning or plan
execution requests and rejects an older backend before sending the task. Update
the backend first and avoid mixed-version serving; raw API clients must make the
same compatibility check because older binaries may ignore unknown JSON fields.

Run status shows the restriction, including after reload. Replay's **Read-only
plan** contains the proposal, source fingerprints and **Start execution**. The
proposal should describe intended actions and acceptance checks; it remains
model-generated advice, not proof that the proposed actions are correct.

Completion stores one `run.plan_recorded` typed event (`read-only-plan-v1`) with
the input, proposal, source identities, integrity hash and 24-hour expiry. It
captures at most 64 explicitly read files; input and proposal are each bounded
at 64 KiB. Each source records root revision, file identity and raw content hash,
not a host path. Missing/truncated or conflicting read evidence fails closed.
Plans with no file reads are viewable but cannot launch execution in this version.

**Start execution** submits `plan_origin:{run_id,hash}` through `POST /api/chat`
in the same owned Conversation. The server reloads the recorded proposal; client
message text cannot replace it. It checks completion, integrity, expiry, current
file-read authorization and source content/root/file identities before creating
a fresh Run with current Agent configuration, Tools, policy and budget. Mode and
owning Agent follow the plan. Changing the Agent requires replanning. The new
Snapshot retains `plan_origin`; the old Snapshot remains read-only.
The execution also retains private-file provenance for the existing network
egress guard, even when ordinary Conversation history is trimmed.

One plan has one execution identity. Repeated starts return `409` with that Run
ID; failed/canceled executions use existing recovery or a new plan, never an
implicit duplicate. Invalid/expired/changed plans return `409`, unknown constraints
or invalid request combinations `400`, and missing/out-of-scope resources `404`.
No user message or execution Run is added on ordinary preflight rejection.

Resume uses the original restriction, not current request settings. Planning
does not enqueue Memory curation from its user message. It still persists normal
conversation, clarification, Context, checkpoint, Trace and Usage records.
Reverify is limited to the recorded input/proposal; a later Conversation answer
cannot complete an older, previously rejected plan. Changed history requires a
new plan rather than silently replacing the verified subject.

## Failure Inventory

| Boundary | Required outcome |
| --- | --- |
| Unknown execution constraint | Reject before creating messages or Runs |
| Model or Skill requests a write | Hide its schema; deny direct invocation before scope resolution, budget or Handler |
| Remote/ambiguous capability | Do not infer read-only authority from concurrency hints or Skill text |
| Ordinary execution | Retain existing Tool authorization and lifecycle |
| Resume/configuration changes | Preserve frozen `plan_only`; recheck current grants |
| Plan completion | Retain input, output/actions, version/hash and explicitly read file identities |
| Execution approval | Create a new Run/Snapshot/budget; never upgrade the planning Run |
| Wrong Workspace, plan hash, expired/incomplete plan | Reject without execution |
| Source content/root/identity changed or access revoked | Reject; request a new plan |
| Repeated/concurrent execute request | One execution identity per plan; report conflict with existing Run ID |
| Stop/crash | Use existing cancellation/recovery; never implicitly start execution |

Source validation covers explicitly read text files, not directory completeness,
external websites, or a transactional filesystem snapshot. Existing per-operation
hash/conflict checks remain necessary after execution starts. Approving a plan
does not authorize a dangerous action or bypass operator policy.

## Validation and Rollback

```bash
TEST_DATABASE_URL='<dedicated-test-postgres-url>' AGENTFLOW_PLANNING_TEST=1 \
  bash scripts/test-browser.sh planning.spec.ts
```

The focused browser gate uses production composition, real file Bindings,
disposable Postgres and a deterministic HTTP provider. It covers all three modes,
Skill-guided forged writes, Task State/command denial, reload, a separate writing
execution, duplicate starts, cross-Workspace rejection and changed materials.
`planning-evidence.json` records inputs, Snapshots, Run/events, source fingerprints,
denials, execution receipts and provider-contract failures. Desktop screenshots
are retained with it in CI's `read-only-planning-evidence` artifact for 14 days.
This proves enforcement/protocol behavior, not live-model plan quality.
The incompatible-backend case substitutes only health metadata and asserts that
no task POST is sent; it does not boot an older server binary.

There is no new table/column or migration command: Snapshot JSON and the existing
Run Event store own the fields. Ordinary Runs are unchanged. Before reverting to
an older binary, drain planning Runs and do not Resume them there: older code
cannot enforce the new restriction. Preserve historical Snapshots and evidence.
