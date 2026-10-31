# Durable steering and follow-up inputs

Steer adds a user constraint to the current Run at the next complete model/Tool
boundary. Follow-up starts a separate Run after successful completion, with a
fresh snapshot and budget. Neither changes Stop, ordinary Send, or frozen policy.
Multi-Agent steering belongs to the orchestration stages, never isolated Workers.

Model-requested questions use [structured clarification](structured-clarification.md),
not steer/follow-up receipts. Answering does not create a follow-up Run or approve a Tool.

## Failure Inventory

| Failure | Expected behavior |
| --- | --- |
| Duplicate key, identical payload | Return the same receipt, including after consumption |
| Duplicate key, changed payload | Conflict; never replace accepted content |
| Wrong owner, missing/closed Workspace | Reject before reading or consuming inputs |
| Withdrawal races consumption | One transaction wins; applied inputs cannot be withdrawn |
| Submission during Tool execution | Finish the entire batch before adding user content |
| Submission during compaction | Load inputs after compaction; retain their source references |
| Submission at final-response boundary | Consume at the final boundary or leave visibly queued |
| Process dies after consumption | Message and receipt commit together; reconstruct from applied inputs |
| Follow-up preparation fails | No message or receipt consumption; remaining inputs stay queued |
| Follow-up crashes after Run creation | Receipt points to exactly one Run; never auto-create another |
| Follow-up references a read-only planning Run | Reject enqueue and legacy dispatch before consuming a receipt or creating a successor; keep steering and explicit plan approval unchanged |
| Stop, failed verification, exhausted budget | Do not automatically start the next task |
| Queue or admission capacity exhausted | Transparent rejection or retained queue; explicit retry |

Applied means included at an execution boundary, not that the model obeyed the
instruction. Context manifests identify steering with source `steering` and the
receipt ID; request captures remain governed by the existing capture policy.

## Usage and API

While a Run is busy, type in the composer and explicitly select **Steer current
run** or **Queue follow-up**. Ordinary Send stays disabled. **Input queue** shows
receipts and queued withdrawals; it survives refresh and tab changes. Applied
follow-ups link to their own Run. Stop cancels only the current Run and leaves
the queue intact. After a failure or cancellation, **Start next follow-up** is an
explicit request, not an automatic retry. Waiting/recoverable Runs must first be
continued, recovered or canceled through their existing controls.

| Endpoint | Purpose |
| --- | --- |
| `GET /api/conversations/{id}/inputs` | Ordered receipts in the authorized Workspace |
| `POST /api/conversations/{id}/inputs` | Accept explicit `steer` or `follow_up` with `run_id`, `content`, `idempotency_key`; follow-up also accepts `mode` and `agent_id` |
| `DELETE /api/conversations/{id}/inputs/{inputID}` | Idempotently withdraw an unconsumed input; applied/expired returns 409 |
| `POST /api/conversations/{id}/inputs/start` | Admit an explicit retry of the oldest follow-up |

Identical normalized requests with the same owner/Conversation/key return the
original receipt, even when applied or withdrawn. Changed payload returns
`409 input_conflict`. Limits: **8 KiB/input, 16 queued inputs, 64 KiB queued
content, 64 KiB total steering/Run, 256 retained receipts/Conversation**.
Overflow returns `429 input_queue_full` with `Retry-After`. Inputs expire after
**24 hours** unless applied. Receipt cleanup becomes eligible after **seven
days**, on the next submission; active/recoverable Run evidence is retained.
The idempotency window ends when the receipt is purged. User messages remain
ordinary Conversation history until that Conversation is deleted.

## Execution and Recovery Boundaries

Steering loads after hard compaction and before each request assembly. It is a
required, separately referenced user message, never a system instruction. Tool
observations are completed before assembling the next request. Live streaming
also checks after a final answer: a newly accepted steer retracts the provisional
answer and starts another bounded request, preserving provider continuation
fields. A steer arriving after that last check remains visibly queued; it is not
silently converted to a follow-up. Parent orchestration text stages apply inputs
before the next request; an isolated Multi-Agent Worker never receives them.
Local simulated answers only have the initial assembly boundary.

Applied steering originals are reloaded for subsequent requests and supported
mode-specific Resume, even if history has been compacted. Their original
application Stage/Turn is preserved. Required inputs still count against the
frozen Context and Run budgets; overflow fails rather than silently dropping a
constraint. The first `context.assembled` event containing a receipt ID identifies
its first assembled model request. A manifest proves request inclusion, not that
the provider accepted it or obeyed it.

Successful completion dispatches follow-ups FIFO, one input per new Run. Each
uses current configuration and a fresh Snapshot, budget and verification state;
it does not inherit the predecessor's completion contract. This adapter reuses
`app/runcommands.Start`, with the same preparation, execution, completion,
admission and single-writer ownership as Chat. It passes authorized scope and
no output callbacks, never a synthetic HTTP request or SSE writer. Workspace
activity and current Membership are checked again inside
consumption transactions. Background work is capacity-bounded and participates
in graceful shutdown; browser disconnect does not own its execution lifetime.

Receipt consumption, the user message and the fresh Run commit atomically. A
preparation failure before that transaction leaves the input queued; failures
after creation point to the existing Run rather than automatically recreating
it. Created Runs use existing mode-specific crash repair/Resume limitations.
After process restart, pending inputs are inspectable and explicitly startable;
there is no unbounded startup dispatcher or automatic replay of side effects.
This remains a **single-instance** execution boundary, not a multi-instance job
queue or a general-purpose scheduler.

### Planning follow-up boundary

Queue follow-up is disabled when read-only planning is selected or the source
Run's frozen constraint is `plan_only`, including after reload. Steering remains
available at the existing boundaries. The server rejects follow-up enqueue with
`409 input_plan_only`, including retries of historical queued requests. Automatic
and explicit dispatch check the receipt's source Run, not just the latest Run in
the Conversation; the consumption transaction enforces the same policy before
writing a message, applying the receipt or creating a successor.

Historical planning follow-ups remain queued and withdrawable; they are never
silently converted, consumed or skipped past FIFO. To execute a plan, use the
separate explicit [plan approval](read-only-planning.md) path. Ordinary follow-ups
are unchanged. This boundary rejects unrepresentable planning intent; it does
not add full successor configuration inheritance.

## Verification

The failure inventory above drives the Postgres integration gate, including
concurrent withdrawal/consumption, transaction rollback, revoked Membership,
archived Workspaces, bounds, expiry, and reopening persisted receipts. Unit
checks cover manifest identity/deduplication and final-answer retraction.
An ordinary backend integration test uses production HTTP/runtime composition
with Postgres and a controlled provider, so inbox dispatch is also checked by
`go test ./...` without starting a browser.

Run the focused browser-to-Go-to-Postgres gate against a dedicated test server:

```bash
AGENTFLOW_SANDBOX_BROWSER_TEST=1 TEST_DATABASE_URL=postgres://.../agentflow_test \
  bash scripts/test-browser.sh run-inbox.spec.ts
```

The fixture controls a whole sandbox Tool batch in Single/Multi/Loop and retains
JSON evidence with Run identities, snapshots, receipts, manifests and explicit
limitations. It measures protocol correctness, not live-model compliance or
real sandbox isolation. No screenshots, mobile checks or live provider calls
are required.

The additional boundary profile enables real compaction and a three-call Run
budget in the isolated server. It verifies steering submitted during a blocked
summary request and that steering cannot reset an exhausted budget:

```bash
AGENTFLOW_INBOX_EDGE_TEST=1 TEST_DATABASE_URL=postgres://.../agentflow_test \
  bash scripts/test-browser.sh run-inbox-boundaries.spec.ts
```

Both gates write machine-readable evidence attachments into the Playwright JSON
report at `apps/web/test-results/results.json`. The first gate has five cases;
the boundary profile has two. They do not verify multi-instance dispatch or
guarantee recovery of modes that the existing Runtime cannot Resume.

`run-inbox-planning.spec.ts` adds two focused browser cases through production
composition and disposable Postgres: planning Queue rejection before/after
reload with steering preserved, and ordinary follow-up dispatch. Run it with
the same dedicated `TEST_DATABASE_URL` and browser command above; no opt-in
provider profile is needed. It retains `planning-inbox-evidence.json` and a
synthetic desktop screenshot. Focused Go/Postgres tests additionally seed legacy
queued receipts, reject automatic/manual dispatch and direct command/transaction
consumption, retain queued state after Store reopen, and check source identity
when a later ordinary Run exists. Existing planning integration checks the
separate approved execution path. These are deterministic protocol checks,
not live-model measurements.
