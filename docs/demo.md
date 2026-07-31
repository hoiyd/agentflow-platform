# Interview Demo

This walkthrough demonstrates the platform in three to five minutes. It uses a
small fictional runbook so exact-identifier recall and semantic recall are both
repeatable. No API key is required for the local workflow path.

## Before the Call

```bash
make quickstart
```

Open `http://localhost:3000`. Upload
[`examples/demo-runbook.md`](../examples/demo-runbook.md) from the **Knowledge**
view. Keep one completed Multi-Agent Run available if the interview environment
has unreliable network access.

## Walkthrough

### 0:00-0:40 - Platform Boundary

Show the three execution modes. Explain that they share one Turn Engine,
retrieval pipeline, tool executor, usage ledger, and event model; each mode owns
only its orchestration policy.

### 0:40-1:40 - Hybrid Retrieval

Search for `AUTH-7F31`. Point out that keyword recall preserves identifiers that
semantic similarity may miss. Then search for `login failures after a key
rotation` to exercise semantic recall. Inspect source details, independent
recall ranks, RRF score, final rank, relevance decision, and selected model
context.

### 1:40-3:00 - Multi-Agent Run

Start a Multi-Agent task:

```text
Use the incident runbook to diagnose authentication failures after a signing-key
rotation. Return the relevant incident code, likely cause, and recovery steps.
```

Show planning, delegated steps, tool activity, and the final answer. If no model
provider is configured, use the deterministic fallback to demonstrate lifecycle
and persistence rather than answer quality.

### 3:00-4:20 - Trace and Replay

Open **View trace**. Connect the visible events to the persisted Run lifecycle:
retrieval, context selection, model/tool operations, usage settlement, and the
terminal state. Show that Replay reads stored evidence rather than reconstructing
the Run from UI state.

### 4:20-5:00 - Engineering Boundaries

Close with two explicit limits: authentication and complete Workspace lifecycle
are not implemented, and relevance thresholds still need calibration against a
versioned Golden Dataset. This distinguishes implemented platform behavior from
planned production hardening.

## Verification Fallback

If live execution is unavailable, run:

```bash
make test
```

The command verifies every Go package, frontend lint, frontend contract tests,
and the Next.js production build. A saved Replay can still demonstrate the
observability model without making a provider request.
