# AgentFlow Internal Terms

This document defines the internal execution terminology used by AgentFlow.
These definitions are architectural contracts: domain models, event names,
APIs, traces, replay data, and UI labels should use them consistently.

## Core hierarchy

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

An autonomous Run may repeat a group of Stages. `Iteration` identifies that
group; it is not another execution entity in this hierarchy.

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

## Stage

A **Stage** is a named orchestration phase inside a Run. Only an orchestrator
creates Stages. Single-agent Runs normally do not have them.

Examples:

- Multi-agent: `planner`, `router`, `worker`, `reviewer`, `finalizer`.
- Autonomous: `observe`, `plan`, `act`, `review`, `decide`.

A Stage records the responsibility currently being performed, its input and
output, status, selected Agent, and optional Iteration number. A Stage usually
contains one Turn, but the relationship is not required to remain one-to-one.
For example, a fallback or retry may create another Turn within the same Stage.
A rule-based router may complete a Stage without creating a Turn.

Model Calls, Tool Calls, Retrievals, retries, and arbitrary implementation
operations are not Stages.

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

An **Iteration** is the numbered repetition of an autonomous Stage sequence.
It is a grouping attribute rather than an independently persisted execution
unit.

```text
Iteration 1: observe → plan → act → review → decide
Iteration 2: observe → plan → act → review → decide
```

The Iteration number belongs on each Stage and related event. An Iteration is
not a Stage, Turn, Model Call, or retry count.

## Model Call

A **Model Call** is one request to an LLM provider and its response. One Turn
may make multiple Model Calls, especially when tools are involved.

A Model Call records provider/model identity, token usage, duration, output,
and failure information. Streaming deltas belong to the active Model Call.

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
of Run, Stage, or Turn.

### Single-agent mode

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

### Multi-agent mode

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

### Autonomous mode

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

## Event namespace

Unified execution events use the entity that owns the activity:

```text
run.*
stage.*
turn.*
model.*
tool.*
retrieval.*
memory.*
```

Overall autonomous budget and iteration status use `run.progress`. Stage events
describe named orchestration phases; they must not use `workflow.*` or overload
`step.*`.

Memory synchronization is an auxiliary Run activity. `memory.sync.*` records
requested, completed, and failed persistence without changing the outcome of a
successfully completed Turn.

## Quick reference

| Term | Question it answers |
| --- | --- |
| Conversation | Which long-lived chat does this belong to? |
| Run | Which user request is the system executing? |
| Stage | Which orchestration phase is active? |
| Turn | Which Agent is processing which input? |
| Iteration | Which autonomous repetition contains these Stages? |
| Model Call | Which LLM request occurred? |
| Tool Call | Which registered capability was invoked? |
| Retrieval | Which context was loaded for the Turn? |
