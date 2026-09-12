# Interview Demo

This walkthrough demonstrates the platform in three to five minutes. It uses a
small fictional runbook so exact-identifier recall and semantic recall are both
repeatable. No API key is required for the local workflow path.

## Before the Call

Start PostgreSQL with pgvector and confirm `DATABASE_URL` in `apps/api/.env`
points to it. The launcher starts AgentFlow, not the database.

```bash
make quickstart
```

Open `http://localhost:3000/workspace`. Upload
[`examples/example.md`](../../examples/example.md) from the **Knowledge** view. This
file is sample knowledge content, not the Demo instructions themselves. Keep one
completed Multi-Agent Run available if the interview environment has unreliable
network access.

In a second terminal, build the fixed CASE-001 benchmark evidence pack:

```bash
make benchmark-evidence
make routing-eval
```

`make benchmark-evidence` writes six machine-readable files under `.cache/benchmark-suite`:
the 12-task manifest, deterministic RAG report, Tool protocol report, Tool
failure events, recovery events, and a saved recoverable Replay. The
offline provider and hash embedder are explicit fixtures; do not present their
results as live-model quality. ACL remains diagnostic because authenticated
identity and document authorization are not implemented.

The routing command runs a separate 16-case calibration/holdout gate against
the production eligibility, ranking, fallback, and abstention code. Keep its
terminal output available for the routing discussion below.

## Walkthrough

### 0:00-0:35 - Platform Boundary

Show **Single**, **Multi**, and **Loop** and name their execution shapes: one
direct Turn; plan/approve/route/work/review/finalize; and bounded
observe/plan/act/review/decide Iterations. Explain that they share one Turn
Engine, retrieval pipeline, Tool executor, Usage Ledger, and event model; each
mode owns only its orchestration policy. Briefly point out that Run admission,
per-Conversation single-writer execution, model-request limits, and Run Budget
remain shared performance and concurrency controls rather than mode-specific
implementations.

### 0:35-1:15 - Configurable Agent Profile and Routing Evidence

In **Single**, open **New agent** or **Configure**. Show that one persisted
profile owns its responsibility, system prompt, Tool allowlist, and Memory/RAG
switches. Explain that starting a Run freezes
the effective profile; Multi additionally freezes all active profiles as Router
candidates, so later edits cannot change Resume or Replay semantics.

Briefly distinguish platform Tool enablement from the per-Agent allowlist. The
Tool Executor still applies timeout, result-size, panic-recovery, tracing, and
concurrency policy after both layers admit a call.

Use the `make routing-eval` output to show that the single Router algorithm
records 10/10 acceptable selections, zero unsafe false routes, and 6/6 no-route
recall on the fixed Dataset v1. The tracked retirement Artifact preserves how
the deleted v1/v2 implementations produced unsafe or missed no-route decisions.
The Dataset recommends score/margin
`4/1`, but production remains at conservative `6/1`; this is the deliberate
decision not to auto-publish a policy from a small offline fixture. No live LLM
quality claim is made.

### 1:15-2:05 - Hybrid Retrieval

Search for `AUTH-7F31`. Point out that keyword recall preserves identifiers that
semantic similarity may miss. Then search for `login failures after a key
rotation` to exercise semantic recall. Inspect source details, independent
recall ranks, RRF score, final rank, relevance decision, and selected model
context.

For an AI-systems-focused review, open
`.cache/benchmark-suite/rag-offline.json`. This is the canonical
`agentflow-rag-baseline@1.2.0` result produced by the same production retrieval
components against the isolated corpus. Point out Hit@1/3/5, per-case misses,
prompt-injection blocks, gating versus diagnostic cases, and the active
Embedding/Fusion/Reranker/Relevance Gate versions. Find `stale-refund-window`:
the expected source is policy `3.2`, policy `2.4` is forbidden, and a weak or
conflicting result remains visible instead of disappearing from the
denominator. The UI searches above validate the interactive path; they are not
the canonical Dataset run.

### 2:05-3:15 - Multi-Agent Run

Start a Multi-Agent task:

```text
Use the incident runbook to diagnose authentication failures after a signing-key
rotation. Return the relevant incident code, likely cause, and recovery steps.
```

Show planning, delegated steps, tool activity, and the final answer. If no model
provider is configured, use the deterministic fallback to demonstrate lifecycle
and persistence rather than answer quality.

### 3:15-4:25 - Trace, Replay, and Episode Report

Open **View trace**. Connect the visible events to the persisted Run lifecycle:
retrieval, context selection, model/tool operations, usage settlement, and the
terminal state. Show that Replay reads stored evidence rather than reconstructing
the Run from UI state. Point out the Episode Report's task, retrieval, LLM,
Tool, error, and Verification summary, then export its JSON as a compact
machine-readable artifact for offline evaluation or incident review.

If the live provider is unavailable, open
`.cache/benchmark-suite/benchmark-recovery-replay.json` instead. It is a
synthetic saved Replay produced through the same HTTP Resume and persistence
contracts, not a screenshot or reconstructed UI object.

### Optional - Runtime Verification

Enable **Verification** for a new Run and select a deterministic
text, citation, JSON Schema, HTTP, or allowlisted command verifier. Show that
the candidate output, Evidence, Artifacts, and `verification.*` events are
persisted before the Completion Gate permits `run.completed`.

Emphasize that this is **verification of one runtime outcome**, not execution
of the repository's unit or integration tests.

### Optional - Controlled Run Comparison

Create a Single Agent with Memory and RAG disabled, then run the same prompt in
two new Conversations without changing its model, Tools, or limits. Open
**Evaluate > Run comparison**, select those Runs, and show that the
Comparability Gate enables token, duration, call-count, and error deltas.

Then select an unrelated Run. The outputs remain available side by side, but
the page lists the changed identities and disables deltas. This demonstrates
that the view is an evaluation surface for repeated or single-variable tests,
not a performance claim over arbitrary production traffic. See
[Evidence Comparison](../operations/evidence-comparison.md) for the complete
gate.

### 4:25-5:00 - Engineering Boundaries

Close with explicit limits: mandatory Workspace namespace filtering and
stale-source replacement are implemented, but authentication, Membership, ACL,
and complete Workspace lifecycle are not; the ACL case therefore remains
diagnostic rather than a release gate. Routing is protected by a deterministic
offline regression gate, but live LLM routing and production threshold promotion
still need budgeted multi-trial evidence. This distinguishes implemented
platform behavior from planned production hardening.

## Recorded README Assets

- `agentflow-demo.gif`: end-to-end Multi-Agent execution and Replay overview.
- `hybrid-rag-demo.gif`: ingestion, Hybrid recall, RRF, reranking, Relevance
  Gate metadata, and final model-context selection.
- `completion-verification-demo.gif`: Completion Contract configuration,
  `passed` status, Usage/Replay, verification lifecycle events, and immutable
  Evidence details.
- `single-mode.png`: direct Single-Agent result with the selected Agent visible.
- `multi-mode.png`: Multi plan approval checkpoint and queued collaboration
  stages.
- `loop-mode.png`: bounded Loop Iteration, resource counters, and Stage trace.

The GIFs explain state transitions. The PNGs provide one stable, distinguishing
state for each execution mode; they are not separate walkthroughs. Focused
recordings remain short and use key state transitions instead of high frame
rates so labels and trace payloads stay readable on GitHub.

## Offline Demo Fallback

If live execution is unavailable, run:

```bash
make benchmark-evidence
make routing-eval
make test
```

The first command produces the fixed benchmark evidence; the second runs every
Go package test, frontend lint, frontend contract tests, and the Next.js
production build. Automated tests validate the codebase; the saved Replay
demonstrates runtime behavior without a provider request.
