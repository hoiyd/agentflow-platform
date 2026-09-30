# AgentFlow Internal Terms

Domain models, APIs, events, Replay, and UI use these names consistently.
Package ownership is in [architecture](backend-architecture.md); control scope
is in [execution controls](../runtime/execution-controls.md).

## Core Hierarchy

```text
Conversation -> Run -> Stage (orchestrated modes) -> Turn
                   -> Turn (Single)
Turn -> Retrieval / logical Model Calls / Tool Calls
```

An Iteration groups Loop Stages; it is not another independently persisted entity.

## Workspace Namespace

Non-empty isolation key for Conversations, Messages, Runs, Documents, Memory,
and their scoped operations. Empty/omitted and legacy `default` normalize to
`default_workspace`, never "all data." It is not an identity, Membership, or ACL.
HTTP receives a mandatory scoped Store view; trusted Runtime/Recovery ID-based
capabilities are not the HTTP persistence interface. User-selected namespaces
need validation at an authenticated boundary before tenant authorization can be claimed.

## Conversation

Long-lived chat container with Messages and multiple Runs. It has no execution
status and does not run models/Tools. A new task normally creates another Run;
responding to an existing human checkpoint resumes that Run.

## Run

One user-request execution: owns Snapshot, Stages/Turns, Events, resource limits,
and overall status. States include `queued`, `running`, `waiting_for_user`,
`canceling`, `canceled`, `completed`, `failed`, and `failed_recoverable`.
A waiting/recoverable Run may continue after validated Resume; completion is
separate from `verification_status`. Browser connection state is not Run state.

## Structured Task State

Conversation-scoped versioned goal, tasks, decisions, constraints, blockers, and
Artifact references. Typed optimistic patches create immutable Revisions; they
do not replace the whole state or change ownership/version metadata. Assembly
loads non-empty current facts and records their version in the Manifest. Replay
exposes the revision timeline. This is durable task context, not another summary
or a Run's mutable lifecycle. See [Task State](../runtime/task-state.md).

## Runtime Snapshot

Immutable execution protocol captured at Run creation: mode, Agent(s)/prompts
and routing inputs, Chat route catalog/affinity and generation profiles,
independent embedding identity, Tool definitions/security/progress, Skill content,
Context Assembly/Compaction config, Loop limits, and Run Budget. Completion
Contracts are also frozen for opted-in Runs. Resume reconstructs this protocol;
Replay returns it rather than current editable configuration.

Credentials are never captured. Current process credentials, request limits,
retry/timeouts, Tool Handlers, and deployment allowlists stay live. See
[model routing](../runtime/model-routing.md) for removed targets and credential
reference checks, and [control ownership](../runtime/execution-controls.md#frozen-protocol-vs-live-policy).

Only `domain.CurrentRuntimeSnapshotVersion` is resumable (currently v18;
[code](../../apps/api/internal/domain/runtime_snapshot.go) is authoritative).
Historical versions or missing Snapshots remain Replay-only; unsupported Resume
returns `runtime_snapshot_resume_unsupported` rather than using live config.
Feature pages should link here instead of copying the current version number.

Execution validates the complete current contract, including Tool security,
schema/revision, progress settings, and route generation policy. It does not
match legacy Tool digests or substitute live policy for missing frozen fields;
historical decoding and database migrations remain separate read/upgrade paths.

## Stage

Named orchestration phase created only by an orchestrator. Multi uses
Planner/Router/Worker/Reviewer/Finalizer; Loop uses Observe/Plan/Act/Review/Decide.
Single normally has none. A Stage records input/output, status, selected Agent,
and optional Iteration. It can have zero Turns (rule-based Router) or multiple
Turns; it is not an arbitrary Model Call, retry, Tool, or retrieval operation.

`CollaborationStep` is the existing persisted/API record for one Stage
occurrence. Its ID is the related event `stage_id`, not a separate execution
layer. Multi Worker isolation does not create a Child Run.

## Turn

One complete Agent execution of defined input, yielding a result or terminal
error. It may make multiple logical Model Calls and Tool Calls: decide on a
weather Tool -> execute Tool -> generate answer is **one Turn**, not two.
Single attaches Turns directly to its Run; orchestrated modes also attach them
to a Stage. Native multi-round Tool continuation stays within the same Turn.

## Iteration

Numbered repetition of the Loop Stage sequence, carried by its Stages/events.
It is a grouping attribute, not a Stage, Turn, Model Call, or retry count.

## Model Call

One logical LLM request/response. Retry Policy may use several physical HTTP
attempts for it; each attempt has request telemetry/capture and reacquires
permits. One Turn may make several logical calls. Streaming deltas belong to
that active call. Do not equate attempts with logical Run usage.

## Usage Ledger

Append-only Run accounting. Reservations/settlements share an operation ID;
provider settlement replaces the estimate in effective totals. Unsettled work
retains conservative reservations; Tool calls use their call ID. Budget/cost
uses this authority, not Trace totals. See [Run Budget](../runtime/run-budget.md).

## Tool Call

Invocation of a registered capability during a Turn, with ID, arguments,
result/error, and duration. One Model Call can request several Tools. Tools do
not create Stages or Turns, and a need for durable receipts is not an external-write class.

## Retrieval

Context lookup for a Turn, including Memory or Knowledge. Automatic pre-Turn
lookup is Retrieval activity, not a Tool Call. Explicit `knowledge_search` or
`knowledge_read` is a Tool invocation through the same scoped capabilities.

## Mode

Mode determines orchestration, not the definitions of Run/Stage/Turn.
[Execution modes](../runtime/execution-modes.md) owns lifecycle and selection guidance.

### Single Mode (`single`)

One Agent Turn, no orchestration Stage; native model/Tool rounds may repeat.

### Multi Mode (`multi_agent`)

Ordered Planner -> plan approval -> Router -> isolated Worker -> Reviewer ->
Finalizer Stages inside one Run. LLM-backed Stages use Turns; a rule-based Router
need not call a model.

### Loop Mode (`autonomous`)

Repeated bounded Observe/Plan/Act/Review/Decide Stages. Human input resumes the
same Run; iterations/output and effective Run Budget prevent unbounded work.

## Event Namespace

Owners emit `run.*`, `stage.*`, `turn.*`, `model.*`, `tool.*`,
`retrieval.*`, `memory.*`, `context.*`, `usage.*`, `budget.*`,
and `verification.*`. Loop-wide iteration/budget state uses `run.progress`;
Stage events do not invent `workflow.*` or overloaded `step.*` namespaces.
The [generated event catalog](../runtime/event-catalog.md) is the exact list.

Memory Candidate proposal/policy and accepted-candidate sync are auxiliary
activities, not completion dependencies; their failure does not invalidate a
successful Turn.

## Observability Records and Views

| Record / view | Meaning |
| --- | --- |
| Run Event | Typed boundary fact; lifecycle events are durable, streaming `model.delta` is not |
| Trace | Human-readable event/payload view, not a second event history |
| Projection | Deterministic event/record-derived read model at a sequence watermark, not a new truth store or Resume checkpoint |
| Replay | Detailed aggregate of Snapshot, Messages, Stages, summary, Ledger, Events, Task State revisions, Verification, and other persisted evidence |
| Episode Report | Compact export derived from Replay; no new execution facts |
| Context Manifest | Source-selection/transform/token metadata, not raw prompt content |
| Model Request Envelope / Capture | Exact physical request identity/hash and policy-controlled optional content, not just reconstructed prompt guesses |
| Operator attention | Derived cross-Run queue of actionable Evidence/Recovery Actions, not another persisted Run status |

The [projection contract](../runtime/event-projections-runtime-invariants.md)
explains watermarks and report-only diagnostics. [Request Capture](../context/model-request-reconstruction.md)
explains privacy and retention. Evaluation compares evidence; runtime Verification
gates one frozen output contract. Neither is another execution entity.
