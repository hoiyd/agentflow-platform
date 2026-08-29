# Interview Demo

This walkthrough demonstrates the platform in three to five minutes. It uses a
small fictional runbook so exact-identifier recall and semantic recall are both
repeatable. No API key is required for the local workflow path.

## Before the Call

```bash
make quickstart
```

Open `http://localhost:3000`. Upload
[`examples/example.md`](../examples/example.md) from the **Knowledge** view. This
file is sample knowledge content, not the Demo instructions themselves. Keep one
completed Multi-Agent Run available if the interview environment has unreliable
network access.

In a second terminal, seed and run the canonical retrieval baseline once:

```bash
make golden-eval
```

The command labels no-answer, ACL, and stale-data cases as diagnostic because
their calibration or policy prerequisites are intentionally unfinished.

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

### 0:35-1:05 - Configurable Agent Profile

In **Single**, open **New agent** or **Configure**. Show that one persisted
profile owns its responsibility, system prompt, Tool allowlist, and Memory/RAG
switches. Explain that starting a Run freezes
the effective profile; Multi additionally freezes all active profiles as Router
candidates, so later edits cannot change Resume or Replay semantics.

Briefly distinguish platform Tool enablement from the per-Agent allowlist. The
Tool Executor still applies timeout, result-size, panic-recovery, tracing, and
concurrency policy after both layers admit a call.

### 1:05-2:00 - Hybrid Retrieval

Search for `AUTH-7F31`. Point out that keyword recall preserves identifiers that
semantic similarity may miss. Then search for `login failures after a key
rotation` to exercise semantic recall. Inspect source details, independent
recall ranks, RRF score, final rank, relevance decision, and selected model
context.

For an AI-systems-focused review, open **Retrieval evaluation** and show the
canonical `agentflow-rag-baseline@1.0.0` result. Point out Hit@1/3/5, per-case
misses, prompt-injection blocks, gating versus diagnostic cases, and the
Embedding/Fusion/Reranker/Relevance Gate versions used by the same production
pipeline.

### 2:00-3:15 - Multi-Agent Run

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

### Optional - Runtime Verification

Enable **Verification** for a new Run and select a deterministic
text, citation, JSON Schema, HTTP, or allowlisted command verifier. Show that
the candidate output, Evidence, Artifacts, and `verification.*` events are
persisted before the Completion Gate permits `run.completed`.

Emphasize that this is **verification of one runtime outcome**, not execution
of the repository's unit or integration tests.

### 4:25-5:00 - Engineering Boundaries

Close with two explicit limits: mandatory Workspace namespace filtering is
implemented, but authentication, Membership, ACL, and complete Workspace
lifecycle are not; the canonical Golden Dataset exists, but relevance and
no-answer thresholds still need calibration and the ACL/stale-data cases are
not release gates. This distinguishes implemented platform behavior from
planned production hardening.

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
make test
```

The command runs every Go package test, frontend lint, frontend contract tests,
and the Next.js production build. These automated tests validate the codebase;
a saved Replay demonstrates runtime behavior without a provider request.
