# Offline Evaluation Reports and Regression Gates

AgentFlow uses one CLI and one provenance envelope for deterministic Context and
RAG checks plus explicitly authorized live Tool-task checks:

```bash
cd apps/api
go run ./cmd/eval context --enforce
go run ./cmd/eval rag --enforce
go run ./cmd/eval tool --live --model MODEL \
  --max-model-calls 30 --max-total-tokens 60000 --trials 3 --enforce
```

Every report uses `agentflow-evaluation-report-v1` identity fields: evaluation
kind, Dataset ID/version/hash, Git revision, start/end time, and an explicit
gate. Domain payload schemas remain separate (`context-quality-eval-v1`,
`rag-eval-v1`, and `task-eval-v1`) so Context selection, retrieval ranks, model
tokens, and Tool calls are not confused.

## Context Quality Gate

The Context runner calls the production Context Assembler against a versioned,
network-free dataset. It checks the final model input for required fact
retention, forbidden stale-content leakage, irrelevant selected-token ratio,
per-source token share, total estimated input tokens, and stable prefix hashes.
Cases place evidence early, in the middle, and late in history and cover
corrections, exact identifiers, unfinished work, history-budget pressure,
required-input overflow, missing sources, and raw-history fallback after a
failed compaction.

The default `compacted_history` strategy uses fixed, reviewed compaction
summaries. This proves assembly, shadowing, budget, and constraint-retention
behavior; it does not claim that a live model can generate summaries of the
same quality or that the final answer is correct. No model request is sent.
Each successful sample hashes a canonical final-input payload using the same
provider-neutral `modelrequest.Observation` contract as production capture,
but the offline report stores no prompt content.

Generate a controlled one-variable comparison with:

```bash
go run ./cmd/eval context --strategy full_history > /tmp/context-baseline.json
go run ./cmd/eval context --strategy compacted_history \
  --baseline /tmp/context-baseline.json --ablation --enforce
```

Reports are comparable only when Dataset hash, model, Assembler version,
Compaction algorithm, and Context Assembly configuration match. `--ablation`
allows only the strategy to differ. Increased gating failures, lower required
fact retention, higher forbidden-content or irrelevant-context ratios, and
stable-prefix drift fail the comparison. Input-token change is reported as a
cost signal rather than treated as quality by itself.

The dataset separates `calibration` and `holdout` cases. Gating cases use exact
invariants: all required facts retained and zero forbidden facts included.
The intentionally missing-source case is diagnostic, remains in the denominator,
and cannot block the canonical gate. No uncalibrated aggregate quality score is
used as a release threshold.

## RAG Gate

The RAG runner builds a clean temporary FileStore from the versioned corpus and
uses the production chunking, hybrid recall, RRF, reranking, relevance, security,
and context-selection path. Local hash embeddings make default runs deterministic
and network-free. The report records actual component identity, Top-K, similarity
threshold, each Case outcome/error, stable source hashes, MRR, binary-relevance
NDCG, Hit@K, no-answer Precision/Recall, leak count, blocked candidates, and latency.
Each sample belongs to an explicit `calibration` or `holdout` split, and the report
publishes metrics for both. Ranked source diagnostics include confidence,
similarity, evidence coverage, and the Gate reason without copying document text.

The current `heuristic-relevance-calibrated-v1` profile removes common English
query stop words and requires at least `0.25` query-term evidence coverage before
weak vector/reranker paths may admit a result. The value was selected on the
calibration split and must also pass the untouched holdout split. It is a baseline
for this corpus and deterministic embedder, not a universal threshold. Compare a
candidate as one explicit ablation:

```bash
go run ./cmd/eval rag --enforce > /tmp/rag-baseline.json
go run ./cmd/eval rag --min-evidence-coverage 0.30 \
  --baseline /tmp/rag-baseline.json --ablation --enforce
```

Cases tagged `non-blocking` remain diagnostic. Every other Case is gating;
failed, canceled, missed, or not-evaluated gating Cases fail `--enforce` and
remain in the denominator. Undefined no-answer metrics are JSON `null`. Timing
and timestamps are observations, not deterministic comparison keys.

`--baseline REPORT` compares only reports with identical Dataset/corpus hashes,
retrieval configuration, and component identity. Incomparable inputs fail the
gate instead of being called an improvement. Increased gating failures or leaks,
and decreased MRR/NDCG, are regressions.

For an intentional single-variable experiment, add `--ablation`. The comparison
is accepted only when exactly one of Top-K, threshold, chunker, retrieval mode,
Embedding, Fusion, Reranker, Relevance Gate, or security policy differs; the
changed field is recorded.

### Semantic retrieval profile

`eval rag` can explicitly replace the deterministic hash embedder with one real
OpenAI-compatible or Ollama model. Live access is opt-in and requires a model,
its expected dimensions, a physical request budget, an estimated input-token
budget, and a deadline. Indexing, queries, and retries share those limits; every
retry consumes another physical request. Missing credentials, invalid vectors,
model or dimension drift, exhausted budgets, and timeouts fail closed and remain
visible in the report denominator. They never fall back to hash.

```bash
cd apps/api
OPENAI_API_KEY=... go run ./cmd/eval rag \
  --embedding-profile openai_compatible --live-embeddings \
  --embedding-base-url https://api.openai.com/v1 \
  --embedding-model text-embedding-3-small --embedding-dimensions 1536 \
  --max-embedding-calls 50 --max-embedding-input-tokens 50000 \
  --embedding-timeout 2m --min-similarity 0.15 \
  --min-evidence-coverage 0.25 --retrieval-mode hybrid --enforce
```

For Ollama, use `--embedding-profile ollama` and an
`--embedding-base-url` ending in `/api/embed`; no API key is required. Run the
same profile and corpus with `hybrid`, `dense_only`, and `lexical_only` to
measure the retrieval contribution. A baseline comparison with `--ablation`
accepts `retrieval_mode` as the sole changed variable. FileStore lexical recall is
the existing token-overlap heuristic, not BM25. Real profiles require both
threshold flags explicitly; the values above are only a runnable starting point,
not a calibrated semantic profile.

The report records requested and actual model identity, dimensions, input
transformations, cosine distance, index identity, separate index/query timing,
and phase-level logical-input and physical-request estimates. Embedding APIs do
not currently return standardized token usage or price, so cost stays JSON
`null` and is never reported as zero. Reports contain hashes and ranked source
metadata, not raw document text or vectors. Real-model runs are manual and are
not a default CI dependency.

## Tool Gate and CI

Tool evaluation remains opt-in because it spends model quota. It records trials,
provider/model, frozen Context configuration, Tool contract hash, verified output,
failure evidence, latency and Usage Ledger totals. Missing usage stays estimated;
unsettled usage stops later samples.

Default CI never calls public models. Fixture-backed Tool protocol checks and the
canonical Context/RAG runs write JSON into `EVALUATION_REPORT_DIR`; the backend
job uploads the directory as `offline-evaluation-reports`. Reports omit
credentials/endpoints, redact model errors, and do not copy Context or RAG source
content.
