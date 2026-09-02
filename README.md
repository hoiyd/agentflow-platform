# AgentFlow Platform

[![CI](https://github.com/hoiyd/agentflow-platform/actions/workflows/ci.yml/badge.svg)](https://github.com/hoiyd/agentflow-platform/actions/workflows/ci.yml)
[![Go Coverage](https://codecov.io/gh/hoiyd/agentflow-platform/branch/main/graph/badge.svg?flag=backend)](https://app.codecov.io/gh/hoiyd/agentflow-platform)

AgentFlow is a full-stack AI agent workflow platform designed to make agent
systems reliable beyond the first demo through explicit orchestration, tool
execution, retrieval, context management, resource control, verification, and
replay.

The backend is written in Go. The frontend is a Next.js workbench for running
and inspecting Single, Multi, and bounded Loop workflows.
The project uses OpenAI-compatible model and embedding APIs, with deterministic
local fallbacks for development.

## Project at a Glance: Three Execution Modes

AgentFlow exposes three orchestration shapes over one shared Go runtime. The
mode changes **how work is coordinated**, not which retrieval, Tool, Context,
Budget, Event, Verification, or persistence policies apply.

### Single (`single`)

For focused questions and direct tool-assisted work. One selected Agent executes
one Turn with the lowest orchestration overhead; retrieval, model/tool loops,
streaming, budgets, and optional Verification still apply.

`User -> Agent Turn -> Result`

![Single mode direct result](docs/assets/single-mode.png)

### Multi (`multi_agent`)

For work that benefits from an explicit plan, specialist routing, and
independent review. The Run pauses at `waiting_for_user` so the plan can be
edited before execution, and every handoff is persisted as an inspectable
Stage.

`User -> Planner -> Approve -> Router -> Worker -> Reviewer -> Finalizer`

![Multi mode plan approval and collaboration stages](docs/assets/multi-mode.png)

### Loop (`autonomous`)

For open-ended work that needs iterative progress within hard limits. Each
Iteration observes, plans, acts, reviews, and decides whether to continue,
stop, or request human input; runtime, output, Tool, and budget limits remain
visible throughout execution.

`User -> [Observe -> Plan -> Act -> Review -> Decide] x N -> Result / Wait`

![Loop mode iteration trace and resource limits](docs/assets/loop-mode.png)

The detailed lifecycle, trade-offs, trace shape, API values, and mode-selection
guide are in [Execution modes](docs/execution-modes.md).

## Example User Story Walkthrough

### End-to-End Multi Mode

![AgentFlow reviewed Multi-Agent workflow](docs/assets/agentflow-demo.gif)

This walkthrough follows one incident task through plan review, worker routing,
execution, review, finalization, and persisted Replay. It demonstrates **Multi**
mode; Single and Loop use the same runtime controls and evidence model with the
different orchestration shapes above. No API key is required.

### RAG Pipeline

![Hybrid RAG retrieval, ranking, and context selection](docs/assets/hybrid-rag-demo.gif)

The focused RAG recording shows document indexing, independent Semantic and
Keyword ranks, RRF fusion, versioned reranking, Relevance Gate metadata, and
the merged model context. The same pipeline runs before Turns in all modes.

### Verification and Trace

![Completion Contract, Completion Gate, evidence, and trace](docs/assets/completion-verification-demo.gif)

The focused runtime recording shows a frozen Completion Contract, a completed
Single-mode Run, Usage/Replay, `verification.*` lifecycle events, and immutable
Evidence. Verification checks one runtime outcome; it does not run the
repository's Automated or Manual Tests. It can be enabled for Single, Multi,
or Loop Runs.

## Why This Project Exists

A chat completion is easy to demonstrate. A platform must also answer harder
questions:

- What exact configuration, context, tools, and limits did this Run use?
- How do multiple agents share one execution model without duplicating logic?
- How are provider retries separated from logical model-call accounting?
- What happens when retrieval returns no confident evidence?
- Which information becomes durable memory, and why?
- Can a result be replayed, audited, resumed, canceled, or rejected by policy?

AgentFlow makes those concerns explicit in persisted domain models, typed Run
Events, frozen Runtime Snapshots, bounded execution policies, and focused
subsystems rather than hiding them inside prompts.

## Architecture at a Glance

```mermaid
flowchart LR
    UI["Next.js workbench"] -->|"HTTP + SSE"| HTTP["Go HTTP adapter"]
    HTTP --> Scope["Workspace scope"]
    Scope --> Runtime["Agent Runtime"]
    Runtime --> Modes["Single / Multi / Loop"]
    Runtime --> Turn["Shared Turn Engine"]
    Turn --> Context["Context Assembly"]
    Turn --> Model["Model adapter"]
    Turn --> Tools["Guarded tools"]
    Runtime --> Events["Typed Run Events"]
    Scope --> Knowledge["Knowledge Base"]
    Knowledge --> Retrieval["Semantic + Keyword -> RRF -> Rerank -> Gate"]
    Retrieval --> Transform["Parent/adjacent selection -> Dedup/Merge"]
    Scope --> Memory["Memory Provider"]
    Memory --> Candidates["Candidate policy"]
    Scope --> Store["File Store / Postgres + pgvector"]
    Events --> Store
    Candidates --> Store
    Transform --> Store
```

All three execution modes share the same Turn, Retrieval, Context Assembly,
Tool, Event, Budget, and Completion paths. Mode-specific code owns only the
orchestration policy; see [Execution modes](docs/execution-modes.md) for the
exact lifecycle of each path.

## Engineering Highlights

The highlights are grouped by ownership so Agent runtime design, knowledge
grounding, and cross-cutting platform controls can be reviewed independently.

### Agent Runtime and State

| Concern | Implementation | Implementation evidence |
| --- | --- | --- |
| [Agent orchestration](docs/execution-modes.md) | Direct Single, plan-and-review Multi, and bounded Loop modes share one Turn Engine | [runtime](apps/api/internal/agent/runtime.go), [tests](apps/api/internal/agent/orchestrator_test.go) |
| [Bounded Child Run delegation](docs/child-run-delegation.md) | Multi-Agent Workers execute in isolated Child Runs with frozen Tool authority, independent budgets, no-wait backpressure, bounded result handoff, cancellation, and replayable parent-child topology | [runtime](apps/api/internal/agent/child_run.go), [tests](apps/api/internal/agent/orchestrator_test.go) |
| [Configurable Agent profiles](docs/agent-profiles.md) | Persisted profiles combine a custom responsibility, system prompt, Tool allowlist, and Memory/RAG switches; Multi freezes active profiles as Router candidates | [API](apps/api/internal/httpapi/agents.go), [tests](apps/api/internal/httpapi/agents_test.go) |
| [Reproducibility](docs/terms.md#runtime-snapshot) | Each Run freezes model, Agent, Tool schema, context policy, and budget in a Runtime Snapshot | [snapshot](apps/api/internal/agent/runtime_snapshot.go), [tests](apps/api/internal/agent/runtime_snapshot_test.go) |
| [Context control](docs/context-management.md) | Per-source budgets, Context Manifests, non-destructive compaction, and a protected recent message tail | [assembler](apps/api/internal/contextassembly/assembler.go), [tests](apps/api/internal/contextassembly/assembler_test.go) |
| [Structured durable task state](docs/task-state.md) | Conversation-scoped goals, tasks, decisions, constraints, blockers, and Artifact references evolve through optimistic typed patches rather than summaries | [domain](apps/api/internal/domain/task_state.go), [Store tests](apps/api/internal/store/file_task_states_test.go) |
| [Durable memory](docs/memory-management.md) | Explicit rules plus optional shadow-mode model extraction propose auditable candidates before embedding | [curator](apps/api/internal/memory/curator.go), [tests](apps/api/internal/memory/curator_test.go) |
| [Provider compatibility](docs/engineering-decisions.md#openai-compatible-does-not-mean-capability-identical) | The native Turn Engine requires structured Tool Calling and reports unsupported models explicitly; stream usage metadata has a bounded transport fallback | [provider adapter](apps/api/internal/openai), [tests](apps/api/internal/openai/context_integration_test.go) |

### RAG and Knowledge Grounding

| Concern | Implementation | Implementation evidence |
| --- | --- | --- |
| [Hybrid retrieval and ranking](docs/knowledge-rag.md#retrieval-pipeline) | Independent semantic and keyword recall feed Reciprocal Rank Fusion, a versioned Reranker, and a separate Relevance Gate | [pipeline](apps/api/internal/rag/retrieval.go), [stage tests](apps/api/internal/rag/retrieval_test.go), [API/Runtime regression](apps/api/internal/httpapi/rag_pipeline_regression_test.go) |
| [Context selection and citations](docs/knowledge-rag.md#parent-child-context-selection) | Source-traceable child hits drive scoped parent/adjacent expansion, deduplication, merging, token selection, and resolved `[S1]` citations | [selection](apps/api/internal/rag/context_selection.go), [citations](apps/api/internal/rag/citations.go), [tests](apps/api/internal/rag/context_selection_test.go) |
| [Retrieval safety and evaluation](docs/rag-golden-dataset.md) | Injection filtering runs before ranking; a versioned Dataset and paired corpus exercise fact, exact-ID, multi-source, no-answer, leakage, and injection cases through the same retrieval path | [guard](apps/api/internal/rag/security.go), [evaluation](apps/api/internal/rag/evaluation.go), [dataset](examples/knowledge/golden-dataset.v1.json) |

### Reliability, Governance, and Evidence

| Concern | Implementation | Implementation evidence |
| --- | --- | --- |
| [**Performance & resource control**](docs/execution-controls.md) | SSE streaming, bounded context/output, asynchronous Memory curation, and per-Run usage/cost budgets keep work measurable and bounded | [budget](apps/api/internal/budget/budget.go), [tests](apps/api/internal/budget/budget_test.go) |
| [**Concurrency & backpressure**](docs/execution-controls.md#1-run-admission-and-conversation-concurrency) | Global Run admission, per-Conversation single-writer execution, bounded queues, model-request permits, RPM/TPM limits, and retry-aware slot release | [controller](apps/api/internal/concurrency/run_controller.go), [tests](apps/api/internal/concurrency/run_controller_test.go) |
| [Workspace namespace isolation](docs/backend-configuration.md#workspace-scope) | Every API request resolves a non-empty Workspace; Conversations, Messages, Runs, Documents, Memory, retrieval, Replay, and Verification remain scoped in File and Postgres stores | [HTTP scope](apps/api/internal/httpapi/workspace.go), [Store contract](apps/api/internal/store/store.go), [tests](apps/api/internal/store/file_store_test.go) |
| [Tool governance](docs/tool-security-policy.md) | Platform enablement, Agent allowlists, frozen capability scope, operator policy, Budget, side-effect recovery, and concurrency remain explicit independent controls | [policy](apps/api/internal/toolpolicy/policy.go), [executor](apps/api/internal/tools/executor.go), [tests](apps/api/internal/tools/security_executor_test.go) |
| [**Verification**](docs/verification.md) | Frozen Completion Contracts run configured deterministic or model-backed verifiers, persist immutable Evidence/Artifacts, and gate `run.completed` | [engine](apps/api/internal/verification/engine.go), [tests](apps/api/internal/verification/engine_test.go) |
| [**Tracing & replay**](docs/backend-architecture.md#performance-concurrency-tracing-and-verification) | Typed Run/Stage/Turn/Model/Tool/Retrieval/Verification events, usage ledgers, Replay, and Episode reports explain what happened | [episode](apps/api/internal/httpapi/episode_report.go), [tests](apps/api/internal/httpapi/episode_report_test.go) |

Performance statements above describe implemented controls, not synthetic
benchmark claims. Verification evaluates configured Run outcomes; Automated and
Manual Tests validate system behavior. The same Run, Event, Budget, and
Verification contracts apply across execution modes and File/Postgres
persistence.

## Three-to-Five-Minute Demo

1. Start the complete local stack. No API key is required for the deterministic
   workflow demonstration:

   ```bash
   make quickstart
   ```

2. Open `http://localhost:3000`. In **Single**, create or edit an Agent and
   point out its system prompt, Tool allowlist, and Memory/RAG switches. The
   same effective profile is frozen when the Run starts.
3. Open **Knowledge** and upload [`examples/example.md`](examples/example.md).
4. Search for that identifier and inspect Semantic rank, Keyword rank, RRF,
   final rerank, and the transformed model context.
5. Run `make golden-eval` to seed the canonical retrieval corpus and expose
   Hit@1/3/5, misses, security decisions, and active pipeline versions.
6. Run a Multi-Agent task against the runbook, then open **View trace** to
   connect orchestration stages, retrieval, model calls, usage, and final Run
   state. Export the Episode Report when a compact machine-readable review
   artifact is useful.

With an OpenAI-compatible key in `apps/api/.env`, the same path exercises a real
model. The key is optional so an interviewer can still inspect runtime behavior,
persistence, and Replay without provider setup or cost.

The timed walkthrough and talking points are in
[`docs/demo.md`](docs/demo.md).

## Engineering Evolution and Evidence

The repository preserves an incremental engineering path rather than presenting
the current design as a one-shot architecture:

1. Retrieval moved from runtime-specific Store calls to one shared pipeline for
   HTTP, Single, Multi, and Loop execution.
2. Semantic-only recall gained an independent keyword path, then rank-based RRF
   fusion so incomparable raw scores were never mixed directly.
3. Retrieved child chunks were separated from model context, enabling scoped
   parent/neighbor expansion, source traceability, deduplication, and merging.
4. Automatic message copying was replaced with conservative Memory Candidates,
   deterministic policy, shadow rollout, redaction, and asynchronous persistence.
5. Execution limits were separated by scope: process admission, physical model
   attempts, logical Run usage, single-call context capacity, and loop guards.
6. Final output evolved from an unqualified completion into an optional,
   evidence-gated Completion Contract with replayable artifacts.

Each step introduced a narrower contract and regression tests before the next
capability depended on it. The rationale and rejected shortcuts are recorded in
[Engineering decisions](docs/engineering-decisions.md).

## Known Boundaries

These boundaries are documented rather than hidden. The supported deployment
scope and its expansion path are documented in the
[Production Readiness Roadmap](docs/production-readiness-roadmap.md).

- Workspace namespace isolation is mandatory: omitted scope resolves to
  `default_workspace`, and Store/RAG paths do not perform unscoped reads.
  Identity, Membership, ACL policy, and Workspace lifecycle are not yet
  implemented, so this is not an authorization or full multi-tenant guarantee.
- The HTTP API does not yet provide an authentication or authorization layer.
  Run it only in a trusted development environment or behind an external access
  boundary.
- Customizable workflow is not yet supported. I tried the Temporal workflow engine, 
  but it didn't bring much benefit considering the large complexity it brings. 
  It'll be a long-term plan.
- Prompt-injection detection is a high-precision deterministic layer, not a
  semantic guarantee. Untrusted-context boundaries remain active even when no
  pattern is detected.
- The canonical Golden Dataset v1 makes retrieval behavior repeatable, but
  relevance and no-answer thresholds remain heuristic until they are calibrated
  and promoted from diagnostic results to release criteria.
- RAG `no_match` prevents weak candidates from entering model context, but it
  does not yet force the model to abstain from answering from prior knowledge.
- The local hash embedding fallback is for deterministic development, not
  retrieval-quality evaluation.
- Verification proves configured invariants; it does not make every
  subjective answer factually correct.
- Progress guards for repeated actions or oscillation, and asynchronous external
  evidence ingestion, are planned rather than implemented.

## Quick Start

### Prerequisites

- Go `1.26.5` managed through `gvm`
- Node.js `22+`
- Optional: Postgres with `pgvector`
- Optional: an OpenAI-compatible API key and Ollama embedding endpoint

### One-Command Local Start

```bash
make quickstart
```

This installs locked frontend dependencies, downloads Go modules, and starts
the API on `http://127.0.0.1:8080` plus the workbench on
`http://localhost:3000`. Press `Ctrl+C` once to stop both processes.

For subsequent runs, skip dependency installation:

```bash
make dev
```

If the default ports are occupied:

```bash
API_PORT=18080 WEB_PORT=13000 make dev
```

### Manual Start

Backend:

```bash
cd apps/api
cp .env.example .env
source ~/.gvm/scripts/gvm
gvm use go1.26.5
GOCACHE=/private/tmp/agentflow-go-build-cache go run ./cmd/server
```

The API listens on `http://127.0.0.1:8080` by default. Set `BIND_ADDRESS`
explicitly when a trusted deployment needs another interface. With no API key, the
backend uses deterministic local behavior suitable for exercising workflows.

Frontend, in another terminal:

```bash
cd apps/web
npm install
npm run dev
```

Open `http://localhost:3000`. Set `NEXT_PUBLIC_API_BASE_URL` only when the API
is hosted elsewhere.

### Automated Tests

```bash
make test
```

The command runs the complete Go package suite plus frontend lint, library
tests, and production build. Postgres integration tests remain opt-in and
require a disposable database through `TEST_DATABASE_URL`.

## Repository Map

```text
agentflow-platform/
  apps/
    api/
      app/                 application composition root
      cmd/server/          process entry point
      internal/            domain capabilities and adapters
    web/                   Next.js workbench
  docs/                    architecture and operational documentation
  packages/shared/         reserved shared-contract boundary
```

## Suggested Review Path

For a short technical review:

1. Compare the three [Execution modes](docs/execution-modes.md).
2. Inspect how [Agent profiles](docs/agent-profiles.md) become Single/Loop
   runtime inputs and frozen Multi Router candidates.
3. Read [Engineering decisions](docs/engineering-decisions.md).
4. Follow the [backend call paths](docs/backend-architecture.md#main-call-paths).
5. Inspect the [retrieval pipeline](docs/knowledge-rag.md#retrieval-pipeline)
   and run the [evaluation harness](docs/api-reference.md#rag-evaluation).
6. Compare [Run Budget](docs/run-budget.md) with single-call
   [Context Management](docs/context-management.md).
7. Open Replay and export its Episode Report after running a Multi or Loop task.

The complete documentation map is in [docs/README.md](docs/README.md).
