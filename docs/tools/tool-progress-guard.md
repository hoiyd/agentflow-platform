# Tool Progress Guard

AgentFlow uses a Run-scoped Tool Progress Guard to stop repeated Tool work that
consumes Budget without changing execution state. It runs in the shared Tool
Executor, so Single, Multi-Agent Child Runs, and Autonomous mode use the same
rules.

## What Is Tracked

Each call receives a stable signature derived from:

- Tool source identity;
- Tool name and frozen definition revision;
- canonical argument hash.

The Guard never persists raw arguments or result content. It compares only:

- typed Tool error code plus error category; or
- SHA-256 of a result from an explicitly `read_only` Tool.

Successful external writes reset the pattern and their result content is never
used to infer idempotency. Cancellations, Budget failures, approval decisions,
security denials, and uncertain side effects are not progress signals.

## Escalation

The default policy is:

| Repeated occurrence | Action | Effect |
| --- | --- | --- |
| 1 | `allow` | Execute normally |
| 2-3 | `warn` | Execute and return a bounded warning to the model |
| 4 | `block_call` | Skip Budget accounting, Handler execution, and side effects |
| 5 | `halt_turn` | Stop the current Turn with `turn_no_progress` |

The Guard also detects bounded `A -> B -> A -> B` oscillation. A changed
argument hash or changed typed failure starts a new pattern; a single change is
not treated as repetition. New user input resets the Guard before an Autonomous
Run resumes.

`TOOL_PROGRESS_GUARD_ENABLED`, `TOOL_PROGRESS_WARN_AFTER`,
`TOOL_PROGRESS_BLOCK_AFTER`, and `TOOL_PROGRESS_HALT_AFTER` configure new Runs.
The effective values are frozen in Runtime Snapshot v12, so Resume does not
adopt changed deployment settings. Snapshot v11 Runs retain their historical
behavior with the Guard disabled.

## Replay And Recovery

Every terminal `tool.completed` or `tool.failed` event stores the bounded Guard
version, rule, action, count, call signature hash, outcome fingerprint, and
whether the Handler executed. A restarted process reconstructs the Run-scoped
bounded history from these terminal events.

Escalations also emit explicit events:

- `tool.guard.warned`;
- `tool.guard.blocked`;
- `turn.no_progress`.

Replay can therefore explain which rule fired, the observed repeat count, and
whether the call was merely warned, blocked before execution, or used to halt
the Turn.
