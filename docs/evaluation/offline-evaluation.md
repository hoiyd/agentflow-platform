# Offline Evaluation Reports and Regression Gates

One CLI shares the `agentflow-evaluation-report-v1` identity envelope; domain
payloads remain separate. Reports bind Dataset/version/hash, Git revision,
configuration, timestamps and gate. A passing fixture is not live-model quality.

From the repository root, after installing dependencies:

```bash
make context-eval
make golden-eval
make routing-eval
make benchmark-evidence
```

These commands need no model access or running application. CLI examples below
run from `apps/api` with the repository Go version active. Live calls are opt-in,
budgeted and never part of default CI. [Evaluation guide](README.md) maps other
evidence paths, including compatibility, tokenization, load and recovery.

## Context Quality Gate

The production Assembler processes a frozen corpus with exact required facts,
forbidden stale content, correction/ID/history-pressure cases and explicit
calibration/holdout splits. Reports include source shares, irrelevant-token
ratio, input estimates, stable prefix and canonical payload hashes; no raw prompt.

```bash
go run ./cmd/eval context --strategy full_history > /tmp/context-baseline.json
go run ./cmd/eval context --strategy compacted_history \
  --baseline /tmp/context-baseline.json --ablation --enforce
```

Fixed reviewed summaries test selection, shadowing, overflow and fallback, not
the model's ability to generate a good summary. The
[live compaction experiment](live-compaction.md) measures that separately.

Comparison requires identical Dataset/model/Assembler/compaction/configuration;
`--ablation` permits only strategy to differ. Increased gating failures or
forbidden/irrelevant content, lost required facts and prefix drift fail the gate.
Input-token savings alone are not quality. Missing-source diagnostics remain in
the denominator without blocking; required/forbidden gating invariants are exact.

## Agent Routing Gate

`eval route` uses production eligibility/ranking, response parsing, fallback and
abstention through an offline adapter, not persisted Runtime Runs. The Dataset
freezes catalog and acceptable Agent sets or explicit no-route outcomes.

Reports separate eligible recall, acceptable top-1, unsafe routes, no-route
Precision/Recall, invalid response, fallback, tokens and latency. Failed/timed-out/
skipped cases remain in denominators; undefined ratios are `null`. Holdout requires
expected outcomes and zero unsafe routes. Calibration recommendations are advisory,
not automatic policy updates; historical algorithms are not selectable.
See [immutable retirement baselines](../../examples/routing/legacy-algorithm-baselines.json).

An explicitly authorized live profile disables retries and bounds suite calls,
tokens and per-sample time:

```bash
go run ./cmd/eval route --live --model MODEL \
  --trials 3 --max-model-calls 50 --max-total-tokens 100000 --timeout 60s
```

Export the required credential before running; the command does not authorize
spend by itself. One model response is not a reliability measurement.

## RAG Gate

The runner builds an isolated corpus using production chunking, hybrid recall,
RRF, reranking, relevance/security and Context selection. Default hash embeddings
are deterministic, not a semantic-quality baseline. Reports expose component
identities, ranked source hashes, per-case outcomes, MRR/NDCG/Hit@K, no-answer
Precision/Recall, leaks, security decisions and latency, separately by split.

```bash
go run ./cmd/eval rag --enforce > /tmp/rag-baseline.json
go run ./cmd/eval rag --min-evidence-coverage 0.30 \
  --baseline /tmp/rag-baseline.json --ablation --enforce
```

The fixture-calibrated default evidence coverage is `0.25`; saturated lexical
scores still require that coverage or an exact alphanumeric ID. The example is
a candidate ablation, not a recommended production threshold.

Only `non-blocking` cases are diagnostic. Other failed/canceled/missed/not-evaluated
cases fail `--enforce`; undefined ratios stay `null`. Baselines must match corpus,
Dataset and pipeline identity. `--ablation` permits exactly one declared variable:
Top-K, threshold, chunker, retrieval mode, embedding, fusion, reranker, gate or
security. Incomparable reports cannot become improvements.

[TEI reranker-only comparison](../knowledge/cross-encoder-reranking.md#single-variable-evidence)
also requires equal per-case candidate-set hashes. Older baselines without that
evidence must be regenerated. Rerank time is separate from query latency; unknown
TEI pricing is not zero.

### Semantic Retrieval Profile

Real embeddings require explicit model/dimensions, physical-request/input-token
budgets and deadline. Index, queries and retries share the limits; each retry
costs another physical request. Missing credentials, invalid vectors, identity
drift, timeout or exhausted budget fail closed, never fall back to hash.

```bash
go run ./cmd/eval rag \
  --embedding-profile openai_compatible --live-embeddings \
  --embedding-base-url https://api.openai.com/v1 \
  --embedding-model text-embedding-3-small --embedding-dimensions 1536 \
  --max-embedding-calls 50 --max-embedding-input-tokens 50000 \
  --embedding-timeout 2m --min-similarity 0.15 \
  --min-evidence-coverage 0.25 --retrieval-mode hybrid --enforce
```

For Ollama use `--embedding-profile ollama` and an `/api/embed` endpoint; no key
is required. Freeze the corpus/model and compare `hybrid`, `dense_only` and
`lexical_only` as single-variable runs. Real profiles require both thresholds
explicitly; example values are starting points, not calibrated results.

Reports retain actual/requested identity, dimensions, input transformations,
distance/index identity, phase times and request/input estimates. Standardized
embedding usage/pricing is unavailable, so cost stays `null`. Reports omit raw
documents/vectors; fixture lexical scoring is token overlap, not BM25.

## Tool Gate and CI

[Tool evaluation](tool-task-evaluations.md) is live/opt-in and records task/trial,
model, Context/Prompt/Tool identity, verified outcome, failures, calls, latency and
Ledger usage. Missing usage stays estimated; unsettled usage stops further samples.

Default CI makes no public model calls. Existing deterministic Context/RAG/routing
and Tool protocol checks write JSON to `EVALUATION_REPORT_DIR`, uploaded as
`offline-evaluation-reports`. Reports omit credentials/endpoints/private Context;
errors pass redaction. Retired routing reports remain read-only, not executed policies.

## CASE-001 Evidence-backed Benchmark Suite

[`benchmark-suite.v1.json`](../../examples/benchmark-suite.v1.json) defines nine
policy/operations retrieval tasks and three long settlement-record tasks. It maps
normal/no-answer/stale/conflicting-source/long-context cases and dependency,
budget, cancellation and recovery failures to existing gates.

`make benchmark-evidence` writes `.cache/benchmark-suite/` (override with an
absolute `BENCHMARK_REPORT_DIR`): frozen suite, `rag-offline.json`, `tool-task-protocol.json`,
failure/recovery JSONL and before/after recovery Replays. The
[demo guide](../guides/demo.md#recovery-evidence) explains what to inspect.
This is explicitly simulated protocol/retrieval/accounting/recovery evidence.

## CASE-001A Live Model Benchmark Evidence

`eval benchmark` orchestrates existing runners: real embeddings for nine retrieval
tasks, same-model `full_context`/`with_tools` for three Tool tasks, and deterministic
versus LLM ranking on the Router Dataset. It produces separate reports, not one
intelligence score. With credentials exported, explicitly authorize the spend:

```bash
go run ./cmd/eval benchmark --live \
  --model CHAT_MODEL --base-url https://api.openai.com/v1 \
  --embedding-profile openai_compatible \
  --embedding-model EMBEDDING_MODEL --embedding-dimensions 1536 \
  --embedding-base-url https://api.openai.com/v1 \
  --trials 3 \
  --tool-max-model-calls 45 --tool-max-total-tokens 250000 \
  --route-max-model-calls 50 --route-max-total-tokens 100000 \
  --max-embedding-calls 50 --max-embedding-input-tokens 50000 \
  --timeout 60s --embedding-timeout 2m \
  --output-dir ../../.cache/live-benchmark/MODEL-REVISION --enforce
```

These are independent **suite-wide** ceilings, not per-task allowances. Output
includes `manifest.json`, `rag-semantic.json`, `tool-context-vs-tools.json`,
`route-query-match.json` and `route-llm-ranking.json`, with hashes, configuration,
trials, failures/skips and improved/unchanged/regressed comparisons.

The evidence-completeness gate requires a traceable failure path and an evaluated
no-gain/regression pair; it does not mean every quality gate passed. Failures,
timeouts, invalid responses and unrun trials stay in their denominators. Unknown
cost and estimated usage remain explicit. Only controlled, retained live reports
support quality/cost claims; never quote fixture percentages as live reliability.
