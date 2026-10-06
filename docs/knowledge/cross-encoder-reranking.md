# Cross-Encoder Reranking

## Failure Inventory

| Failure | Expected behavior |
| --- | --- |
| Mode disabled | Existing heuristic ranking, no reranker HTTP requests |
| Invalid mode, endpoint or missing model/revision | Fail configuration; no silent default |
| Empty candidate set | Empty decisions with implementation identity, no HTTP |
| Oversized input | Reject before network; no silent truncation |
| Service error or invalid JSON | Classified failure without provider body or private input |
| Missing, duplicate, unknown index or invalid score | Reject the complete result, not partial Top-K |
| Unsorted response or equal scores | Deterministic descending score / input-index ordering |
| Timeout, cancellation or full concurrency slots | Bounded failure; no retry or heuristic fallback |
| Score is high but scope/evidence is wrong | Existing scope, injection checks and relevance gate still apply |
| Provider returns different text | Only index/score are consumed; original Pipeline content remains authoritative |
| Evaluation enables model requests unintentionally | Explicit live-reranking authorization required |

## Configuration and Serving Boundary

The default remains `heuristic`; no reranker service is needed. To opt in, deploy
an operator-owned Hugging Face [Text Embeddings Inference (TEI) reranking model](https://huggingface.co/docs/text-embeddings-inference/en/supported_models)
following its [serving guide](https://huggingface.co/docs/text-embeddings-inference/en/quick_tour),
pin the model revision in that deployment, then configure the backend:

```dotenv
RERANKER_MODE=tei
RERANKER_BASE_URL=http://127.0.0.1:8082
RERANKER_MODEL=<deployed-model-id>
RERANKER_REVISION=<pinned-model-revision>
RERANKER_TIMEOUT=3s
RERANKER_MAX_CONCURRENT_REQUESTS=2
RERANKER_API_KEY=
```

Restart the backend after configuration changes. The adapter calls `/rerank`
with `query`, ordered `texts`, `raw_scores=false`, `return_text=false`, and
`truncate=false`, using TEI's [request contract](https://github.com/huggingface/text-embeddings-inference/blob/main/router/src/http/types.rs).
Do not put `/rerank` or `/v1` in the origin. An optional key is sent as a Bearer
token only; it is not part of metadata or reports. Remote origins require HTTPS.
Explicit loopback/private literal IP origins permit local HTTP. Exact-origin
egress rejects redirects, proxies, and unauthorized resolved destinations.

This is an inference data-export boundary, not a public Tool egress grant:
the service receives the normalized query and scoped chunk text, potentially
private Knowledge. Only operators choose the destination. Tool private-data
egress restrictions do not prevent this configured inference call. Use a local
or explicitly trusted service and review its retention policy.

`RerankerInfo` reports `cross_encoder`, adapter version `tei-cross-encoder-v1`,
provider `tei`, model, and a configuration fingerprint of origin, model,
revision, timeout, and concurrency. TEI's score response has no artifact
identity: these are declared deployment settings, not attested loaded weights.
Changing service weights behind an unchanged configuration is operator error
and cannot be detected here. Reranker configuration is process-wide, not frozen
in the Run Snapshot; Resume uses current retrieval configuration.

## Algorithm and Limits

The existing sequence remains scoped Semantic + Keyword recall -> injection
checks -> RRF -> reranker -> relevance gate -> expansion/context selection.
The adapter scores the entire fused candidate set in one batch, validates full
index coverage and finite `[0,1]` scores, sorts descending, breaks ties by input
index, then returns the requested Top-K. Source content, scope, index identity,
and recall evidence stay Pipeline-owned; echoed provider text is ignored.

Bounds are 40 candidates (20 per recall arm), 1 MiB encoded input, 64 KiB response,
and the configured per-call timeout, including response body reads. Empty input
returns empty decisions and identity without HTTP. Full concurrency slots fail
immediately; there is no queue, retry, truncation, or heuristic fallback.
Concurrency is per process and separate from Chat/embedding admission. Rerank
calls do not consume LLM generation/token budgets or Usage Ledger entries;
service pricing/usage is unknown rather than reported as zero.

Normalized model scores are ranking signals, not calibrated probabilities or
proof of factual correctness. The existing evidence-coverage relevance gate
is unchanged and may still reject semantic-only paraphrases. A score of `1`
cannot override Workspace scope, injection checks, or evidence requirements.

Search APIs return safe reranker codes: overload is 503, timeout is 504, upstream
service/auth/protocol failure is 502, invalid local input is 400. Raw service
bodies never enter errors. Optional automatic Knowledge retrieval retains the
existing Run behavior: continue without Knowledge and record `rag_error` in
`retrieval.completed`; this is not successful retrieval or a ranking fallback.
Knowledge Tool failures use the existing Tool error path. The current Knowledge
and Replay diagnostics already display reranker metadata, so no new UI/API DTO
is required.

## Single-variable Evidence

From `apps/api`, create fresh baseline and candidate reports with the same
dataset, corpus, embedding profile, recall mode, thresholds, and Top-K:

```bash
go run ./cmd/eval rag > /tmp/rag-heuristic.json
go run ./cmd/eval rag --reranker tei --live-reranking \
  --reranker-base-url http://127.0.0.1:8082 \
  --reranker-model '<deployed-model-id>' \
  --reranker-revision '<pinned-model-revision>' \
  --baseline /tmp/rag-heuristic.json --ablation --enforce \
  > /tmp/rag-cross-encoder.json
```

These commands use deterministic hash embeddings for plumbing checks, not
production semantic quality. For real-quality claims use the same explicit
[semantic embedding profile](../evaluation/offline-evaluation.md#semantic-retrieval-profile)
on both runs and a representative held-out Golden Dataset. `--live-reranking`
explicitly authorizes exporting its candidate text; it does not authorize live
embeddings. Each sample makes at most one rerank batch, with no retry. Query
latency excludes index build; `rerank_latency_ms` isolates the reranker call.

Reports retain `candidate_set_hash` over ordered pre-rerank text hashes, stable
source/version/scope/offset identity, query, limit, and recall scores/ranks.
Random fixture IDs and timestamps are excluded. Reranker-only comparison requires
matching hashes for every Case: missing or different inputs suppress deltas and
fail the comparison gate. Regenerate older baselines without candidate evidence.
Compare Hit@K/MRR/NDCG, no-answer behavior, leakage and total query latency;
inspect per-sample rerank latency. `cost_source` remains unavailable because TEI
does not report priced usage. Account for deployment cost separately.

## Reproducible Checks

```bash
# From apps/api: controlled HTTP contracts and real Pipeline/Run integration.
go test ./internal/rag -run '^TestCrossEncoder' -count=1
go test ./internal/httpapi -run '^TestCrossEncoderPipeline' -count=1
go test ./internal/evaluation/rageval -run '^TestCrossEncoderEvaluation' -count=1

# From apps/web; TEST_DATABASE_URL must be a dedicated disposable Postgres.
AGENTFLOW_RERANKER_TEST=1 npm run test:e2e -- reranker.spec.ts
```

The browser gate exercises production composition and actual Postgres ingest,
search, safe failure/timeout rendering, and reload. Its attached JSON evidence
records inputs, index/result identities, reranker configuration, failure codes,
and the fixture limitation. No visual/screenshot checks are involved. Controlled
protocol tests do not demonstrate a live model quality gain. A pinned live TEI
service and holdout latency/cost/quality evidence are required before recommending
a default switch; AgentFlow does not install/download models or embed serving.
Search error notices include the safe source/code so an operator can distinguish
unavailable service, timeout, overload, and invalid response without raw bodies.
