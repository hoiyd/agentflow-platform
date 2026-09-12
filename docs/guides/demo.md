# Five-minute Interview Demo

This walkthrough presents AgentFlow as a verifiable, recoverable, and bounded
AI Agent Runtime. It follows one evidence task from configuration to evaluation
instead of touring every screen.

## Demo Contract

- **Online path:** a configured model provider, PostgreSQL with pgvector, and the
  workbench show a newly executed Run. Model quality claims require a saved
  CASE-001A live report.
- **Offline fallback:** deterministic fixtures produce Replay and regression
  evidence without a model provider. They demonstrate protocol behavior, not
  live-model quality.
- **Do not mix the two:** a fixture pass proves wiring and invariants; only a
  budgeted multi-trial report supports model-quality or cost claims.

## Prepare Before the Call

In one terminal, start the application:

```bash
make quickstart
```

In a second terminal, generate the network-free backup pack:

```bash
make benchmark-evidence
```

Open `http://localhost:3000/workspace`, then upload
[`examples/example.md`](../../examples/example.md) in **Knowledge**. Configure an
Agent with RAG enabled and `get_current_time` allowed. In **Verification**, enable
the Text verifier with one attempt, `Wait for user`, and these required phrases:

```text
AUTH-7F31
401 invalid_signature
```

If a budgeted CASE-001A run is available, keep its `manifest.json`,
`tool-context-vs-tools.json`, and `route-llm-ranking.json` open. Run the preflight
after both services are ready:

```bash
LIVE_BENCHMARK_DIR=.cache/live-benchmark/MODEL-REVISION make demo-check
```

Without network or provider access, validate only the fallback artifacts:

```bash
DEMO_MODE=offline make demo-check
```

## The Five-minute Script

### 0:00-0:30 - State the System Boundary

Open **Chat** and say:

> AgentFlow is a Go runtime for bounded Agent execution. Single, Multi, and Loop
> have different orchestration policies, but share one Turn Engine, Context
> assembly, Tool executor, Usage Ledger, event protocol, and Completion Gate.

Point to the mode selector only. Do not explain every control. State that Run
admission, per-Conversation single-writer execution, request limits, and Run
Budget bound resource use before work starts.

### 0:30-1:20 - Create One Evidence-backed Run

Select **Multi** and submit:

```text
Use the authentication incident runbook to diagnose login failures after a
signing-key rotation. Return the incident code, the observed error, likely cause,
and recovery steps. Use get_current_time to include the current UTC time. If the
runbook is insufficient, say so instead of guessing.
```

Approve the plan. Show the selected Agent and candidate evidence or abstention.
Explain that the Run freezes effective Agent profiles, model/provider identity,
Context assembly settings, Tool definitions and policy, budgets, and Verification
contract. Later configuration edits therefore cannot change Resume semantics.

### 1:20-2:35 - Connect Execution to Evidence

Open **View trace** and make four connections:

1. Retrieval events identify the selected runbook chunks and final model context.
2. Model and Tool events show physical calls, duration, errors, and returned
   evidence without treating streaming deltas as durable history.
3. Usage shows model calls, Tool calls, input/output tokens, active runtime, and
   whether provider usage was estimated.
4. Verification Evidence records the Text result before the Completion Gate
   permits `run.completed`.

Export the **Episode report**. It is the compact review artifact joining task,
retrieval, model calls, Tool calls, errors, final output, and Verification; the
event log remains the detailed execution record.

### 2:35-3:35 - Show Recovery Without Demo-only Runtime Code

The `make benchmark-evidence` command injects the existing recoverable-run test
scenario. Open these files side by side:

- `.cache/benchmark-suite/benchmark-recovery-before-replay.json`
- `.cache/benchmark-suite/benchmark-recovery-replay.json`

In the first, show `failed_recoverable`, the reason execution stopped, durable evidence,
and the enabled `Resume run` Recovery Action. In the second, find `run.resumed`,
the recovery Stage, committed `checkpoint.captured` events, and the terminal
`completed` state. Then point to `recovery-paths.jsonl`: the integration test
exercises Resume through the HTTP/SSE and persistence contracts, including stale
or unsafe duplicate-action rejection. No production-only failure switch is added.

If an actual recoverable Run is already present in PostgreSQL, use its **Run
replay** page instead and click the projected Recovery Action. Keep the fixture
pair as the no-network backup.

### 3:35-4:35 - Present Measured Comparison, Not a Vague Claim

Open the CASE-001A `manifest.json`. Show the frozen Dataset hash, Git revision,
model identities, trial count, suite-wide budgets, report hashes, and retained
failed/skipped samples. Then open:

- `tool-context-vs-tools.json` for the same-model `full_context` versus
  `with_tools` comparison;
- `route-llm-ranking.json` beside `route-query-match.json` for deterministic
  selection versus explicitly enabled LLM ranking;
- `rag-semantic.json` for retrieval and no-answer evidence.

Report verified success, unsafe false routes, no-route/no-answer behavior,
input/output tokens, calls, latency, fallback, and failures separately. Mention
an unchanged or regressed pair before any improvement. Do not collapse the
reports into one intelligence score.

If no live report exists, use `.cache/benchmark-suite/rag-offline.json` and say
explicitly that it is deterministic regression evidence. Do not quote it as
live-model quality.

### 4:35-5:00 - Close With Boundaries

Close with four limits: authentication/Membership/ACL are not implemented;
process-local concurrency is not a multi-instance scheduler; uncertain external
side effects require reconciliation rather than an exactly-once claim; and the
small frozen datasets measure known cases, not general model intelligence.

The final sentence is:

> The design goal is not maximum autonomy. It is bounded execution whose result,
> cost, failure, and recovery path can be inspected and reproduced.

## Optional Follow-ups

- Open **Evaluate > Run comparison** only for two prepared identical or
  single-variable Runs. Uncontrolled pairs stay review-only and receive no deltas.
- Show a failed Verification and `Wait for user` only when the interviewer asks
  about human review.
- Show Knowledge rank details only when the discussion moves into retrieval.

## Recorded Assets

- `agentflow-demo.gif`: end-to-end Multi execution and Replay.
- `hybrid-rag-demo.gif`: ingestion, Hybrid recall, RRF, reranking, Relevance Gate,
  and final Context selection.
- `completion-verification-demo.gif`: Completion Contract, Verification Evidence,
  Usage, and Replay.
- `single-mode.png`, `multi-mode.png`, and `loop-mode.png`: stable mode-specific
  states for a no-network screen-share fallback.

The recordings explain state transitions; they are not separate demos or
benchmark evidence.
