# Bounded Tool Progress

Tool progress is a display channel owned by the existing Executor, Tracer and
Run Event system. It is distinct from the [Progress Guard](tool-progress-guard.md),
which detects repeated Tool calls/results, and from a Tool's final observation.
No new environment switch, Tool schema option or execution state machine is required.

## Binding Contract

A Binding may report a complete semantic update through its execution context:

```go
tool.ReportProgress(ctx, domain.ToolProgressUpdate{
    Phase: "reading",
    Message: "Reading indexed chunks",
    Completed: &completed,
    Total: &total,
})
```

The Executor injects the optional sink and owns Run/Stage/Turn/call identity.
Single has no invented Stage. Bindings with no progress remain unchanged.
Outside an equipped Executor, the function returns `false` and does nothing.
`true` means accepted for coalescing, **not** committed or delivered.

- Phase: 1-64 lowercase ASCII letters, digits, `_` or `-`.
- Counts: omit both when unknown; otherwise `0 <= completed <= total`, with
  `total > 0` and at most JavaScript's safe integer limit. Counts describe units
  within a phase, not overall task completion. Percentages are not guessed.
- Messages: complete, operator-safe semantic statements, never arbitrary stdout,
  raw private results, credentials split across updates, heartbeats or logs.
- Registered progress events require a real Turn and a matching active Tool call.
  A saved update does not authorize retries, clear reconciliation, satisfy
  Verification, or prove the external side effect succeeded.

## Fixed Bounds

Limits apply per Handler execution, independently of Run Budget and result limits:

| Boundary | Limit / behavior |
| --- | --- |
| Producer attempts | 1,024; further calls return `false` |
| Input message | Reject invalid UTF-8 or more than 8,192 bytes before redaction |
| Published message | Credential redaction, then at most 1,024 UTF-8 bytes; explicit `truncated` |
| Pending buffer | One latest replacement; intermediate updates may be coalesced |
| Publish cadence | First update immediately; then every 250 ms; pending update flushed at Handler return |
| Published updates | At most 32 |
| Published message bytes | At most 16 KiB in total; event identity/phase/count metadata is separately bounded |
| Read model | Latest committed update for at most 32 recent calls |

The sink does not wait for a browser or create a publisher goroutine. The
Executor consumes replacements while waiting for its original Handler. The
existing Event Hub disconnects a lagging subscriber rather than blocking
execution; reconnect uses the durable event cursor and projection.

Cancellation, timeout and Handler return revoke the sink. Retained contexts
cannot emit late progress. This does not make a non-cooperative Go Handler
forcibly stoppable; Bindings must still honor cancellation and their execution
boundary. Sandbox command termination/cleanup remains owned by the sbx Runner.

## Persistence and UI

`tool.progress` is a typed, bounded durable fact in the existing `run_events`
table. It is committed before publication; no database schema migration or
stdout-chunk table is added. `projection.tool_progress` folds the latest update
with real Tool lifecycle events:

- `running`: active execution, not inferred from the phase text or counts;
- `completed`: invocation returned normally, not proof the entire Run/task passed;
- `failed`: normal typed Tool failure;
- `interrupted`: cancellation, timeout, crash repair, an old update preceding
  Resume, or an unfinished call in a stopped Run.

Chat and Replay show a compact expandable **Tool progress** region only when
updates exist. Each entry shows its phase, optional counts/message, actual
execution status and durable sequence. Initial Chat streams retain ownership of
answers/status; the existing authenticated Run event subscription adds committed
progress. Refresh/reconnect restores the same projection, not a new execution.

Display persistence failures are logged as `tool_progress_record_error`; they
do not replace a Tool result/error or instruct a retry. Uncommitted updates are
never advertised as saved. Progress does not enter model messages or Context;
large complete output continues to use [Result Artifacts](tool-result-artifacts.md).
Existing Workspace/owner authorization and Conversation deletion apply to these
Run events and projections as well.

## First Integration and Evidence

`sandbox_command` reports actual `provisioning`, `executing`, `cleaning_up` and
`cleanup_confirmed` boundaries. Short phases may coalesce; no fake ticking or
stdout-derived percentages are produced. Future API or MCP Bindings can use the
same context sink without a dedicated frontend component; this does not add MCP
support or grant new execution capabilities.

Focused browser gate, using a dedicated disposable Postgres database:

```bash
AGENTFLOW_SANDBOX_BROWSER_TEST=1 npm --prefix apps/web run test:e2e -- \
  sandbox-command.spec.ts tool-progress.spec.ts
```

Set `TEST_DATABASE_URL` as described in the [functional regression guide](../operations/functional-regression-testing.md).
The gate covers all three modes, live updates, refresh, completion/cancellation,
Replay reload and real identities. Retained `tool-progress-evidence.json`
attachments contain the input, frozen configuration, Run identity, events,
projection, receipts and limitations.
Separate backend integrations cover slow subscribers, burst/message/byte bounds,
malformed updates, redaction, Handler errors/panic, timeout, late writes, display
commit failure, SIGKILL/Postgres reopen, foreign-Workspace reads and cascade deletion.
Browser evidence uses deterministic provider/sbx CLI fixtures, not a live
microVM or live-model quality measurement.
