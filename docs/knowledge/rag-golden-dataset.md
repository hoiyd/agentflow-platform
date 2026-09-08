# RAG Golden Dataset v1

AgentFlow's canonical retrieval dataset is
[`agentflow-rag-baseline@1.2.0`](../../examples/knowledge/golden-dataset.v1.json).
It is a small, versioned engineering baseline rather than a claim of
domain-wide retrieval quality. The paired corpus lives under
[`examples/knowledge/golden-v1`](../../examples/knowledge/golden-v1).

## Coverage

| Case | Capability exercised | Expected evidence | Gate status |
| --- | --- | --- | --- |
| `fact-control-plane-region` | Direct fact recall | Platform location | Gating |
| `paraphrase-audit-retention` | Query paraphrase | Audit retention fact | Gating |
| `exact-id-px-2049` | Exact identifier / keyword recall | Incident code and operator action | Gating |
| `multi-hop-atlas-oncall` | Multi-source retrieval | Service owner and team schedule | Gating |
| `no-answer-zz-0000` | Unsupported query abstention | No accepted retrieval result | Calibration gate |
| `no-answer-qx-9999` | Unseen unsupported query abstention | No accepted retrieval result | Holdout gate |
| `acl-external-key-reset` | Restricted-source leakage | Public procedure; restricted runbook forbidden | Diagnostic |
| `stale-refund-window` | Superseded-source leakage | Current policy; retired policy forbidden | Gating |
| `injection-release-signing` | Prompt-injection filtering | Safe guide; hostile note forbidden | Gating |

The ACL case carries the `non-blocking` tag and remains diagnostic until
identity-derived ownership and ACL enforcement (RAG-004/RAG-016B) are implemented.
The stale-data case is gated because the corpus ingests policy 2.4 and then atomically
replaces the same `source_key` with policy 3.2. No-answer is gated by one
calibration case and one untouched holdout case. Workspace namespace filtering
(RAG-003) is complete, but it cannot decide whether a caller is authorized for
that namespace. Diagnostic misses must be reported, but must not be represented
as release-gate regressions yet.

The multi-hop case sets `required_source_count: 2`. The evaluator therefore
uses the rank at which both required evidence sources have appeared. This
measures multi-source retrieval only; answer synthesis and claim correctness
need separate answer-level evaluation.

## Run locally

Run the Dataset from the repository root. The command creates a temporary
FileStore, indexes the canonical corpus, runs the production retrieval pipeline
with deterministic local hash embeddings, emits JSON to stdout, and removes the
store. It does not require a running API, database, network, or API key.

```bash
make golden-eval
```

To archive a report or compare it with the next candidate:

```bash
cd apps/api
go run ./cmd/eval rag --enforce > /tmp/rag-baseline.json
go run ./cmd/eval rag --baseline /tmp/rag-baseline.json --enforce > /tmp/rag-candidate.json
```

The same Dataset can be pasted into **Knowledge -> Retrieval evaluation** in the
workbench after the corpus files have been indexed. The text area expects the
Dataset object itself; the frontend wraps it in the API request.

The CLI can also run this fixed corpus through a real semantic embedding profile.
It already separates calibration and holdout cases and covers paraphrase,
exact-ID, hard-negative/no-answer, stale-source, restricted-source, and hostile
source behavior. This avoids maintaining a second corpus whose only difference
would be the embedder. See [Semantic retrieval profile](../operations/offline-evaluation.md#semantic-retrieval-profile)
for the opt-in command and retrieval-mode comparison procedure.

## Interpretation

- Hit@K uses answerable Cases as its denominator. A multi-source Case is a hit
  at the rank where its configured number of expected sources has been found.
- A no-answer Case passes only when the Relevance Gate returns no result.
- Every canonical Case is tagged `calibration` or `holdout`; threshold selection
  uses the former and release acceptance checks gating cases in both splits.
- A forbidden source anywhere in returned Top-K fails the Case.
- `blocked_candidates` is supporting evidence that the prompt-injection guard
  removed hostile material; the injection Case also forbids that source from
  the accepted results.
- Evaluation measures the retrieval pipeline against this corpus. Runtime
  Verification evaluates an Agent Run's configured completion contract; the two
  subsystems are intentionally independent.
- MRR is the mean reciprocal `best_rank` for answerable cases. NDCG uses binary
  relevance at that rank; no-answer cases are excluded from both denominators.
- No-answer Precision/Recall are `null` when their denominator is empty rather
  than being reported as a false zero. Retrieval failures and canceled cases
  remain `failed` or `not_evaluated` in the total and fail gating cases.
- Reports include Dataset/corpus hashes, Git revision, actual chunker, Embedding,
  Fusion, Reranker, Relevance Gate and security-policy identity. Ranked evidence
  includes bounded scores, confidence and Gate reasons plus stable chunk hashes;
  corpus text is not copied.
- The `0.15` dense threshold and `0.25` evidence-coverage setting are calibrated
  for the deterministic baseline only. A real embedding profile must use the
  calibration split to select its threshold, then report holdout results without
  retuning on them; merely reusing the hash threshold is not a semantic-quality
  claim.

## Version discipline

Do not silently edit a published baseline. Changes to Case meaning, expected
sources, or corpus content require a new Dataset and corpus version. The runner
hashes both assets and refuses to describe reports as comparable when Dataset,
corpus, Top-K, threshold, chunker, or pipeline identity differs. Git history and
CI artifacts are the version record; no Evaluation Registry service is required.
