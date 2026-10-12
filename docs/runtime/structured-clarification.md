# Structured clarification

When essential information is missing, an Agent can call `request_user_input`.
The Run pauses, releases execution capacity, and stores its questions and model
continuation in Postgres. A user answers in Chat; refresh and process restart
do not discard the question or answer. Resume continues the **same Run, frozen
Snapshot and remaining budget**, not a fresh task.

## Mode boundaries

| Mode | Where clarification is allowed | After answering |
| --- | --- | --- |
| Single | Root Agent's Tool loop; no fabricated Stage | Continue the pending Tool protocol |
| Multi-Agent | Parent Planner only, never an isolated Worker | Finish planning, then retain the separate plan-approval step |
| Autonomous | Act Tool loop, or the existing Decide `ask_user` response | Continue the saved Act/iteration, or the next iteration after textual input |

The root harness adds this Tool when Tool Calling is available, subject to
Workspace availability. **Existing Workspaces may need to enable it in Tools**;
adding a Tool never silently grants it to an existing owner. No new environment
switch is required. Ordinary text questions in Single/Multi are not parsed into
forms. The model must choose the structured Tool; local simulated responses
cannot prove real-model question selection.

## Durable protocol

1. Call the clarification Tool **alone**. A mixed batch is rejected before any
   Binding runs. Validate the proposal, then commit questions, private continuation,
   Turn closure and `waiting_for_user` together. Waiting does not consume active
   runtime or hold a model permit/Run worker.
2. Commit answers and one user message atomically, using question ID, version,
   authenticated owner and idempotency key. Identical retries return the same
   receipt; conflicting answers never replace accepted content.
3. Admit Resume through the existing Conversation single-writer, atomically claim
   the saved answer, and append it as the matching Tool result. Preserve prior
   observations, supported provider fields (including absent/empty reasoning),
   source references and counters; reassemble against the existing context budget.

Question states are `pending → answered → applied`, or `withdrawn`. Reads expose
`expired` for overdue pending questions and `canceled` after Stop. `applied` means
accepted into resumed execution, **not** that the model obeyed or completed it.
Replay shows questions, answers, original Stage/Turn/call and application time.
Existing Needs Attention uses the Run's waiting state; no second waiting runtime.

Limits: **1–3 questions, 0–6 options/question, 64-byte IDs, 2,000-byte question
text, 500-byte options, 8 KiB aggregate questions or answers, 16 receipts/Run,
256 KiB private continuation, 24-hour pending-question lifetime**. Free text is
always supported even with options. OpenAPI string lengths describe character
limits; server validation additionally enforces UTF-8 byte budgets.

## API and recovery

All paths are under `/api/runs/{id}/clarifications` and use existing Workspace
authorization. See [OpenAPI](../../api/openapi.yaml) for generated DTOs.

| Method / suffix | Purpose |
| --- | --- |
| GET | Ordered question/answer receipts; never private protocol bytes |
| POST `/{question_id}/answers` | Save `version`, `idempotency_key` and question-ID-to-answer map |
| POST `/{question_id}/resume` | Continue from the saved receipt through the existing SSE adapter |
| DELETE `/{question_id}` | Idempotently withdraw a pending question by version and cancel the Run |

Answer storage and execution admission are separate: an overloaded/failed Resume
does not lose the saved answer. Wrong version, canceled/expired questions, an old
question or repeated successful Resume are rejected. Once SSE starts, execution
errors use its error frame rather than changing the HTTP status.

After a crash following claim, stale-run repair can mark the Run recoverable;
Chat and Replay resume its latest applied receipt. Finished stages/output are
reused. Uncertain effects or newer committed writes without a finished recovery
boundary fail closed: this feature does not promise arbitrary side-effect replay
or global exactly-once execution. Stop closes the suspended Stage as well as Run.
Clarification counts as a Tool call; Resume consumes additional model calls from
the same ledger. Question/answer/state events publish only after their transaction commits;
`clarification.answered` is durable, not a live-only progress update.

## Trust and evidence

Answers are user data, never system instructions or action approval; they cannot
expand Tool capability, Workspace grants or sandbox scope. Known credential-topic
questions and recognized credential values are rejected. This is not semantic
protection against disguised solicitation; never enter secrets. Private checkpoint
bytes contain model context, follow existing server/DB access protections, and are
not exposed by question APIs or events. Receipt retention follows Run/Conversation
deletion; no new general-purpose retention service is introduced.

The [functional gate](../operations/functional-regression-testing.md) includes
`clarification.spec.ts`: browser → production Go → disposable Postgres for all
three modes, reload, free text and Replay receipts, with retained JSON evidence.
Production integration additionally tests application restart, claimed-answer
recovery, Stop/withdrawal, exhausted budgets, corrupted checkpoints and legacy
Autonomous input; store tests cover owner/revocation, rollback and transaction
races. Strict provider fixtures verify actual resumed message pairs and context
manifests/captures. These are protocol checks, not live-model quality measurements.

Steering/follow-up remains [a separate user-initiated inbox](durable-inputs.md).
No nonblocking questions, default answers, credential forms, generalized approval
platform, isolated Worker waiting, or new Child Run is added.
