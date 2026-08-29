# Execution Modes

AgentFlow supports three orchestration modes over one runtime: **Single**,
**Multi**, and **Loop**. The API values remain `single`, `multi_agent`, and
`autonomous`; the UI uses Direct, Coordinate, and Bounded loop as supporting
labels.

Mode is frozen in the Run's Runtime Snapshot. It determines the Stage/Turn
topology and resume protocol, but it does not create a separate implementation
of retrieval, tools, context assembly, events, resource accounting, completion,
or persistence.

## Comparison

| Dimension | Single | Multi | Loop |
| --- | --- | --- | --- |
| Primary use | Focused request handled by one Agent | Planned work with delegation and review | Open-ended work requiring iterative progress |
| API `mode` | `single` | `multi_agent` | `autonomous` |
| UI label | Direct / Single agent | Coordinate / Multi-agent | Autonomous / Bounded loop |
| Topology | One direct Turn | Fixed collaboration Stages | Repeated Iterations of fixed Stages |
| Human checkpoint | None by default | Required after Planner, before routing/execution | Optional when Decide identifies missing input |
| Agent selection | One requested/default Agent | Candidate Agents are frozen; Router selects the Worker | One requested/default Agent performs the loop Stages |
| Completion | Direct candidate output | Finalizer candidate output | Decide final answer or bounded fallback output |
| Main strength | Low orchestration overhead | Explicit responsibility and independent review | Iterative correction with hard stopping conditions |
| Main cost | No independent planner/reviewer | More model calls and one required pause | Highest potential call/token/runtime consumption |

## Single Mode

Single mode handles one request with one selected Agent and one direct Turn.
There are normally no orchestration Stages.

```text
Run
`-- Turn
    |-- retrieve Memory and Knowledge
    |-- assemble bounded context
    |-- Model Call
    |-- zero or more guarded Tool Calls
    `-- final Model output
```

The Turn Engine may perform more than one Model Call when the model selects
Tools, but the whole model/tool exchange is still one Turn. Output streams over
SSE while typed retrieval, context, model, Tool, usage, and completion events
are persisted.

Choose Single when:

- the task has one clear owner and does not need a separately approved plan;
- response latency and orchestration overhead matter more than independent
  review;
- retrieval or Tools are useful, but multiple specialist Agents are not.

Single is not a reduced-policy path. Run admission, per-Conversation
single-writer execution, Runtime Snapshot, Run Budget, Verification,
Replay, and Memory curation remain active.

## Multi Mode

Multi mode implements a fixed, inspectable plan-and-review workflow:

```text
Planner -> waiting_for_user -> Router -> Worker -> Reviewer -> Finalizer
             approve/edit
```

1. **Planner** creates an explicit plan and success criteria.
2. The Run enters `waiting_for_user`; the user may approve or edit the plan.
3. **Router** scores the frozen candidate Agents and selects one Worker. Auto
   routing uses the model when available and falls back to deterministic
   query/profile matching.
4. **Worker** executes the approved plan in an isolated, bounded Child Run using
   the selected Agent profile.
5. **Reviewer** evaluates the Worker result against the task and plan.
6. **Finalizer** synthesizes the candidate final output.

Each responsibility is a Stage. Its persisted record is a
`CollaborationStep`, and its lifecycle emits typed `stage.*` Run Events. Replay
therefore shows the approved plan, routing reason and candidate scores, selected
Worker, review, final answer, and per-Stage latency.

Choose Multi when:

- a user must inspect the plan before execution;
- specialist routing or explicit delegation improves the result;
- independent review is worth additional latency and model usage;
- handoffs and responsibility boundaries must be explainable afterward.

The Worker Child Run has its own frozen Tool allowlist, Context, Budget, Usage
Ledger, timeout, heartbeat, and Trace. The parent receives only a bounded
summary and Child Trace reference. See
[Bounded Child Run Delegation](child-run-delegation.md).

The current topology is intentionally fixed rather than a generic arbitrary
DAG. This keeps lifecycle, continue semantics, and Replay predictable while
still exposing a bounded Router extension point.

## Loop Mode

Loop mode is the bounded iterative path. It repeats one five-Stage Iteration:

```text
Observe -> Plan -> Act -> Review -> Decide
   ^                                  |
   +--------- continue ---------------+
```

- **Observe** summarizes current state, constraints, risks, and missing facts.
- **Plan** chooses the next concrete action for this Iteration.
- **Act** executes the plan using the selected Agent and available Tools.
- **Review** checks progress and remaining gaps.
- **Decide** returns `continue`, `stop`, or `ask_user` with a reason and optional
  final answer.

Every Stage carries an Iteration number. `run.progress` exposes current
iteration, elapsed active runtime, output characters, Tool usage, effective
limits, and stop reason.

Loop execution is bounded by:

- maximum Iterations and accumulated output characters;
- effective active-runtime and Tool-call caps resolved into Run Budget;
- model-call, token, and optional estimated-cost Run Budget dimensions;
- provider, Tool, and verifier operation timeouts;
- cancellation and per-Conversation single-writer ownership.

When Decide requests information, the Run persists a human-input checkpoint
and enters `waiting_for_user`. Resume continues the same Run from its frozen
Runtime Snapshot and saved Steps. Startup recovery can mark a stale running Run
`failed_recoverable`; the recovery path resumes from persisted loop state.

Choose Loop when:

- the next action depends on the result of the previous action;
- iterative review can improve an initially incomplete result;
- the work may need a human checkpoint without discarding prior progress;
- explicit stopping conditions can bound the task safely.

Loop is not an unbounded background scheduler. The current implementation is a
bounded in-process execution protocol; distributed scheduling and semantic
progress/oscillation guards remain outside the implemented boundary.

## Shared Runtime Contract

The modes deliberately converge below orchestration:

| Shared capability | Behavior in all modes |
| --- | --- |
| Run lifecycle | Admission, queueing, Conversation single-writer ownership, cancellation, and persisted status |
| Runtime Snapshot | Frozen mode, Agent(s), provider/model identity, Tool schemas, Context policy, and Run Budget |
| Turn Engine | Retrieval, Context Assembly, model/tool loop, usage reservation/settlement, and typed events |
| RAG and Memory | The same scoped retrieval pipeline and durable-Memory policy feed every Turn |
| Workspace namespace | A Run inherits its Conversation Workspace; retrieval, persistence, Replay, and Verification preserve that scope in every mode |
| Tracing | Run/Stage/Turn/Model/Tool/Retrieval/Context/Usage/Verification Run Events use one schema |
| Verification | The same optional frozen contract gates the candidate output from Single, Multi, or Loop |
| Replay and Episode Report | File and Postgres stores expose the same persisted evidence and projections |

This boundary prevents a feature from working in one mode while silently
bypassing policy in another. Adding a provider, Store, Reranker, Tool, or
Verifier changes an adapter or capability implementation rather than the three
mode protocols.

## Choosing a Mode

Use the least complex mode that matches the coordination requirement:

1. Start with **Single** for one-owner work.
2. Choose **Multi** when plan approval, specialist routing, or independent
   review is part of the requirement.
3. Choose **Loop** only when the task genuinely needs repeated
   observe/act/review decisions.

More orchestration is not automatically more capable. Multi and Loop trade
additional calls, latency, and state for stronger coordination or iterative
control.

For entity semantics, see [Internal terms](terms.md). For limits and ownership,
see [Execution controls](execution-controls.md). For package boundaries, see
[Backend architecture](backend-architecture.md).
