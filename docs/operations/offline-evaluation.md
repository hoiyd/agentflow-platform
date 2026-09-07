# Offline Evaluation Reports and Regression Gates

AgentFlow uses one CLI and one provenance envelope for deterministic RAG checks
and explicitly authorized live Tool-task checks:

```bash
cd apps/api
go run ./cmd/eval rag --enforce
go run ./cmd/eval tool --live --model MODEL \
  --max-model-calls 30 --max-total-tokens 60000 --trials 3 --enforce
```

Every report uses `agentflow-evaluation-report-v1` identity fields: evaluation
kind, Dataset ID/version/hash, Git revision, start/end time, and an explicit
gate. Domain payload schemas remain separate (`rag-eval-v1` and `task-eval-v1`)
so retrieval ranks are not confused with model tokens or Tool calls.

## RAG Gate

The RAG runner builds a clean temporary FileStore from the versioned corpus and
uses the production chunking, hybrid recall, RRF, reranking, relevance, security,
and context-selection path. Local hash embeddings make default runs deterministic
and network-free. The report records actual component identity, Top-K, similarity
threshold, each Case outcome/error, stable source hashes, MRR, binary-relevance
NDCG, Hit@K, no-answer Precision/Recall, leak count, blocked candidates, and latency.

Cases tagged `non-blocking` remain diagnostic. Every other Case is gating;
failed, canceled, missed, or not-evaluated gating Cases fail `--enforce` and
remain in the denominator. Undefined no-answer metrics are JSON `null`. Timing
and timestamps are observations, not deterministic comparison keys.

`--baseline REPORT` compares only reports with identical Dataset/corpus hashes,
retrieval configuration, and component identity. Incomparable inputs fail the
gate instead of being called an improvement. Increased gating failures or leaks,
and decreased MRR/NDCG, are regressions.

For an intentional single-variable experiment, add `--ablation`. The comparison
is accepted only when exactly one of Top-K, threshold, chunker, Embedding, Fusion,
Reranker, Relevance Gate, or security policy differs; the changed field is recorded.

## Tool Gate and CI

Tool evaluation remains opt-in because it spends model quota. It records trials,
provider/model, frozen Context configuration, Tool contract hash, verified output,
failure evidence, latency and Usage Ledger totals. Missing usage stays estimated;
unsettled usage stops later samples.

Default CI never calls public models. Fixture-backed Tool protocol checks and the
canonical RAG run write JSON into `EVALUATION_REPORT_DIR`; the backend job uploads
the directory as `offline-evaluation-reports`. Reports omit credentials/endpoints,
redact model errors, and do not copy RAG document text.
