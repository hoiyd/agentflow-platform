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
Document access policy (RAG-045) is enforced using distinct authenticated principals.
The stale-data case is gated because the corpus ingests policy 2.4 and then atomically
replaces the same `source_key` with policy 3.2. No-answer is gated by one
calibration case and one untouched holdout case. Authenticated Workspace access
is enforced separately; it does not implement per-Document ACLs inside a Workspace.
Diagnostic misses must be reported, but must not be represented
as release-gate regressions yet.

The multi-hop case sets `required_source_count: 2`. The evaluator therefore
uses the rank at which both required evidence sources have appeared. This
measures multi-source retrieval only; answer synthesis and claim correctness
need separate answer-level evaluation.

## Run locally

Run the Dataset from the repository root. The command creates an isolated
in-memory fixture, indexes the canonical corpus, runs the production retrieval pipeline
with deterministic local hash embeddings, and emits JSON to stdout.
It does not require a running API, database, network, or API key.

```bash
make golden-eval
```

To archive a report or compare it with the next candidate:

```bash
cd apps/api
go run ./cmd/eval rag --enforce > /tmp/rag-baseline.json
go run ./cmd/eval rag --baseline /tmp/rag-baseline.json --enforce > /tmp/rag-candidate.json
```

The same Dataset can be pasted into the dedicated **Retrieval evaluation** page
(linked from Knowledge) after the corpus files have been indexed. The text area
expects the Dataset object itself; the frontend wraps it in the API request.

The CLI can also run this fixed corpus through a real semantic embedding profile.
It already separates calibration and holdout cases and covers paraphrase,
exact-ID, hard-negative/no-answer, stale-source, restricted-source, and hostile
source behavior. This avoids maintaining a second corpus whose only difference
would be the embedder. See [Semantic retrieval profile](offline-evaluation.md#semantic-retrieval-profile)
for the opt-in command and retrieval-mode comparison procedure.

## Metadata regression

[`agentflow-metadata-regression@1.0.0`](../../examples/knowledge/metadata-golden-v1/dataset.json)
adds 12 gating cases with a separate corpus: identical queries across products and
environments, missing fields, inherited Chunk arrays, typed number/date/boolean
conditions, bounded OR, no match, and draft/withdrawn/expired/future exclusions.
Existing Store parity tests cover conflicting Document/Chunk labels and exact
validity boundaries; the Golden profile exercises production retrieval and Context
expansion, with fixture, PostgreSQL and browser regression gates.

```bash
# From apps/api; no API key, running server or model calls required.
go run ./cmd/eval rag \
  --dataset ../../examples/knowledge/metadata-golden-v1/dataset.json \
  --corpus-manifest ../../examples/knowledge/metadata-golden-v1/corpus-manifest.json \
  --enforce > /tmp/rag-metadata.json
```

Cases may supply `metadata` (legacy text equality), `metadata_filter` (the existing
[typed contract](../knowledge/typed-metadata-filters.md)), and `require_filter: true`.
Case and request restrictions are intersected; conflicting legacy keys are rejected.
Workspace, schema eligibility and lifecycle always remain outside user OR clauses.
All case constraints are preflighted before the first query; malformed/null/unknown
constraints or an omitted required filter fail instead of broadening retrieval.
Old datasets intentionally omitting these optional fields retain unfiltered behavior.

Each case's `evidence` records applied restrictions, policy/schema versions,
observation time, source revisions, Context selection/merge counts and source Chunk
IDs. `chunk_hash` hashes the selected content, including merged Context. Inspect
these in **Retrieval evaluation → Applied constraints and source revisions**.
The corpus keeps expired/future bounds far from the test date; production still uses
the current clock, not an evaluation-supplied time. No Document ACL or live-model
quality improvement is claimed. Semantic embedding profiles remain explicitly opt-in.

## Interpretation

- Hit@K uses answerable Cases as its denominator. A multi-source Case is a hit
  at the rank where its configured number of expected sources has been found.
- A no-answer Case passes only when the Relevance Gate returns no result.
- Every canonical Case is tagged `calibration` or `holdout`; threshold selection
  uses the former and release acceptance checks gating cases in both splits.
- A forbidden source anywhere in returned Top-K or selected Context fails the Case.
- `eligible_source_recall` counts distinct expected sources found in accepted
  retrieval, divided by all declared expected sources (including failed/not-evaluated
  cases). It is `null` without expected sources. Context matches are recorded
  separately; `context_leak_count` counts selected entries that violate scope,
  lifecycle, typed/legacy constraints or forbidden-source expectations.
- Per-case latency covers retrieval and Context selection; `context_tokens` is a
  local estimate of selected Context, not billed model usage. CLI embedding usage
  remains separately attributed to index/query phases; no answer is generated.
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
  Fusion, Reranker, Relevance Gate, security and metadata-eligibility policy identity. Ranked evidence
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
Regenerate older reports without eligibility identity before comparison. HTTP
upstream failures abort the batch without success metrics; the offline runner
retains failed/not-evaluated cases and closes the gate.
