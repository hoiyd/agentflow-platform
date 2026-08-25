# AgentFlow Internal Terms

This document defines the internal execution terminology used by AgentFlow.
These definitions are architectural contracts: domain models, event names,
APIs, traces, replay data, and UI labels should use them consistently.

Use this page when reviewing lifecycle code or traces. For package ownership,
see [Backend architecture](backend-architecture.md); for control ownership, see
[Execution controls](execution-controls.md).

## Core Hierarchy

```text
Conversation
└── Run
    ├── Stage (orchestrated modes only)
    │   └── Turn
    │       ├── Retrieval
    │       ├── Model Call
    │       └── Tool Call
    └── Turn (single-agent mode)
        ├── Retrieval
        ├── Model Call
        └── Tool Call
```

A Loop (`autonomous`) Run may repeat a group of Stages. `Iteration` identifies
that group; it is not another execution entity in this hierarchy.

## Workspace Namespace

A **Workspace Namespace** is the non-empty isolation key carried by persisted
Conversation, Message, Run, Document, and Memory records and their scoped
operations. An omitted scope and the legacy value `default` normalize to the
reserved `default_workspace` namespace; they never mean "search all data."

Workspace scope limits which records a Store operation can read or mutate. It
is not an identity credential, Membership decision, or ACL. An untrusted
deployment must derive or validate it at an authenticated boundary before
treating namespace isolation as tenant authorization.

HTTP adapters operate through a mandatory `WorkspaceScope` and a scoped Store
view. Trusted Runtime and Recovery code may use internal ID-based capabilities,
but those capabilities are not part of the HTTP persistence interface.

## Conversation

A **Conversation** is the long-lived container for the user-visible chat.
It owns the message history and may contain multiple Runs created by separate
user requests.

A Conversation:

- preserves context across user and assistant messages;
- groups Runs that belong to the same chat;
- has no running, failed, or completed execution state of its own;
- does not execute models or tools.

Example:

```text
Conversation
├── Run 1: analyze the project
├── Run 2: improve the backend
└── Run 3: add tests
```

## Run

A **Run** is one execution instance created to handle a user request. It owns
the overall lifecycle, runtime snapshot, resource limits, Stages, Turns, and
events for that execution.

Typical Run states include:

```text
running
waiting_for_user
completed
failed
canceling
canceled
```

Normally, a new user request creates a new Run. An answer to an autonomous
human-input checkpoint resumes the existing Run instead of creating another
one.

A Run may contain one Turn or many Stages and Turns, depending on its Mode.

## Structured Task State

**Structured Task State** is the Conversation-scoped, versioned projection of
the current goal, tasks, decisions, constraints, blockers, and Artifact
references. It is durable execution context, not another summary and not part
of one Run's mutable lifecycle.

Every update is an immutable Revision produced by a typed patch with optimistic
version checking. Context Assembly injects the latest non-empty version before
each Model Call and records that version in the Context Manifest. Replay exposes
the Revision timeline; a Model or client cannot replace the entire state or
change its ownership and version metadata.

## Runtime Snapshot

A **Runtime Snapshot** is the immutable execution protocol captured when a Run
is created. Resume and recovery reconstruct the runtime from this snapshot
instead of reading the current editable configuration.

It includes the Run mode, Agent configuration and system prompt, candidate
Agents for multi-agent routing, provider/model identity, executor, tool names
and schemas, router mode, Loop (`autonomous`) limits, and the Run Budget. Replay
returns the same snapshot so an operator can compare what was configured with
what happened.

A Runtime Snapshot never contains API keys, authorization headers, provider
secrets, or other credentials. The current process supplies
credentials when it reconnects to the frozen provider endpoint. A legacy Run
without a snapshot remains replayable but cannot be resumed safely.

## Stage

A **Stage** is a named orchestration phase inside a Run. Only an orchestrator
creates Stages. Single-agent Runs normally do not have them.

Examples:

- Multi-agent: `planner`, `router`, `worker`, `reviewer`, `finalizer`.
- Loop (`autonomous`): `observe`, `plan`, `act`, `review`, `decide`.

A Stage records the responsibility currently being performed, its input and
output, status, selected Agent, and optional Iteration number. A Stage usually
contains one Turn, but the relationship is not required to remain one-to-one.
For example, a fallback or retry may create another Turn within the same Stage.
A rule-based router may complete a Stage without creating a Turn.

Model Calls, Tool Calls, Retrievals, retries, and arbitrary implementation
operations are not Stages.

`CollaborationStep` is the persisted domain/API record for one Stage
occurrence. Its `id` is used as `stage_id` by related Run Events. It is not a
separate execution layer below or above Stage; the different name preserves the
existing storage and API contract.

## Turn

A **Turn** is one complete Agent execution: an Agent receives a defined input,
performs zero or more Model Calls and Tool Calls, and produces one result or a
terminal error.

```text
Turn: answer a weather question
├── Model Call: decide to use the weather tool
├── Tool Call: retrieve weather data
└── Model Call: produce the final answer
```

The entire example is one Turn, not two. A Model Call is an internal inference
attempt; it is not a Turn by itself.

A Turn belongs directly to a Run in single-agent mode. In an orchestrated mode,
it normally also belongs to a Stage.

## Iteration

An **Iteration** is the numbered repetition of a Loop Stage sequence.
It is a grouping attribute rather than an independently persisted execution
unit.

```text
Iteration 1: observe → plan → act → review → decide
Iteration 2: observe → plan → act → review → decide
```

The Iteration number belongs on each Stage and related event. An Iteration is
not a Stage, Turn, Model Call, or retry count.

## Model Call

A **Model Call** is one logical request to an LLM provider and its response. One
logical call may contain multiple physical provider attempts under Retry Policy.
One Turn may make multiple Model Calls, especially when tools are involved.

A Model Call records provider/model identity, token usage, duration, output,
and failure information. Streaming deltas belong to the active Model Call.

## Usage Ledger

A **Usage Ledger** is the append-only accounting record for one Run. Model
reservations and settlements share a stable operation ID. Effective totals use
provider settlement when available and retain conservative estimates as open
reservations when a call ends before settlement. Tool executions use their tool
call ID as the operation ID.

The ledger is the authority for Run Budget and cost accounting. Trace summaries
remain an event-derived presentation of observed execution activity.

## Tool Call

A **Tool Call** is one invocation of a registered tool requested during a Turn.
It includes a call ID, tool name, arguments, result or error, and duration.

One Model Call may request multiple Tool Calls. Tool Calls do not create Stages
or Turns by themselves.

## Retrieval

A **Retrieval** is a context lookup performed for a Turn, such as persistent
memory search or RAG document search. Its results become input context for the
Turn.

Retrieval is not a Tool Call unless the model explicitly invokes a registered
retrieval tool. AgentFlow's automatic pre-Turn memory/RAG lookup is recorded as
Retrieval activity.

## Mode

A **Mode** selects how a Run is orchestrated. It does not change the definitions
of Run, Stage, or Turn. This section defines entity shape; see
[Execution modes](execution-modes.md) for lifecycle behavior, trade-offs, and
selection guidance.

### Single Mode (`single`)

The user request is handled directly by one Agent Turn. There is no orchestration
Stage.

```text
Conversation
└── Run
    └── Turn
        ├── Retrieval
        ├── Model Call
        ├── Tool Call
        └── Model Call
```

### Multi Mode (`multi_agent`)

The Run is divided into named collaboration Stages. Each LLM-backed Stage
normally executes one Turn.

```text
Conversation
└── Run
    ├── Stage: planner   → Turn
    ├── Stage: router    → Turn or rule-based decision
    ├── Stage: worker    → Turn
    ├── Stage: reviewer  → Turn
    └── Stage: finalizer → Turn
```

### Loop Mode (`autonomous`)

The Run advances through a bounded, repeating set of Stages. The Run owns the
overall limits and may pause for human input before resuming.

```text
Conversation
└── Run
    ├── Iteration 1
    │   ├── Stage: observe → Turn
    │   ├── Stage: plan    → Turn
    │   ├── Stage: act     → Turn
    │   ├── Stage: review  → Turn
    │   └── Stage: decide  → Turn
    └── Iteration 2
        └── ...
```

## Event Namespace

Unified execution events use the entity that owns the activity:

```text
run.*
stage.*
turn.*
model.*
tool.*
retrieval.*
memory.*
context.*
usage.*
budget.*
verification.*
```

Overall Loop budget and iteration status use `run.progress`. Stage events
describe named orchestration phases; they must not use `workflow.*` or overload
`step.*`.

Memory curation is an auxiliary Run activity. `memory.candidate.*` records the
proposal and policy decision for an explicitly durable user fact.
`memory.sync.*` records embedding and persistence of an accepted Candidate.
Neither activity changes the outcome of a successfully completed Turn.

## Observability Records and Views

These names describe different layers over the same Run; they are not
interchangeable stores:

| Concept | Role |
| --- | --- |
| Run Event | Typed execution fact emitted at the boundary where an action occurs. The durable sink persists lifecycle events but intentionally omits streaming `model.delta` events. |
| Trace | Human-readable view of live and persisted Run Events and their payloads. It does not create a second event history. |
| Replay | Detailed per-Run aggregate assembled from persisted records: Runtime Snapshot, Messages, Collaboration Steps, Trace Summary, Usage Ledger, durable Run Events, Structured Task State revisions, and Verification records. |
| Episode Report | Compact projection derived from Replay for review, export, or offline evaluation. It summarizes existing records and introduces no new execution facts. |

## Quick Reference

| Term | Question it answers |
| --- | --- |
| Conversation | Which long-lived chat does this belong to? |
| Structured Task State | Which versioned task facts remain active across Runs and compaction? |
| Run | Which user request is the system executing? |
| Runtime Snapshot | Which immutable execution protocol did the Run capture? |
| Stage / `CollaborationStep` | Which orchestration phase is active, and which record persists that occurrence? |
| Turn | Which Agent is processing which input? |
| Iteration | Which Loop repetition contains these Stages? |
| Model Call | Which LLM request occurred? |
| Tool Call | Which registered capability was invoked? |
| Retrieval | Which context was loaded for the Turn? |
| Usage Ledger | Which resources were reserved, settled, and charged? |
| Run Event | Which typed execution fact was emitted, and was it durable or stream-only? |
| Trace | How are Run Events presented for inspection? |
| Replay | Which detailed records reconstruct this Run? |
| Episode Report | Which compact projection summarizes this Run? |
