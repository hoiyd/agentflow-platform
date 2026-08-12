# RAG Golden Dataset v1

AgentFlow's canonical retrieval dataset is
[`agentflow-rag-baseline@1.0.0`](../examples/knowledge/golden-dataset.v1.json).
It is a small, versioned engineering baseline rather than a claim of
domain-wide retrieval quality. The paired corpus lives under
[`examples/knowledge/golden-v1`](../examples/knowledge/golden-v1).

## Coverage

| Case | Capability exercised | Expected evidence | Gate status |
| --- | --- | --- | --- |
| `fact-control-plane-region` | Direct fact recall | Platform location | Gating |
| `paraphrase-audit-retention` | Query paraphrase | Audit retention fact | Gating |
| `exact-id-px-2049` | Exact identifier / keyword recall | Incident code and operator action | Gating |
| `multi-hop-atlas-oncall` | Multi-source retrieval | Service owner and team schedule | Gating |
| `no-answer-zz-0000` | Unsupported query abstention | No accepted retrieval result | Diagnostic |
| `acl-external-key-reset` | Restricted-source leakage | Public procedure; restricted runbook forbidden | Diagnostic |
| `stale-refund-window` | Superseded-source leakage | Current policy; retired policy forbidden | Diagnostic |
| `injection-release-signing` | Prompt-injection filtering | Safe guide; hostile note forbidden | Gating |

ACL, stale-data, and no-answer cases carry the `non-blocking` tag. They remain
diagnostic until identity-derived ownership and ACL enforcement (RAG-004,
RAG-016B), an explicit freshness policy, and Relevance Gate/no-answer
calibration (RAG-015, RAG-020) are implemented. Workspace namespace filtering
(RAG-003) is complete, but it cannot decide whether a caller is authorized for
that namespace. Diagnostic misses must be reported, but must not be represented
as release-gate regressions yet.

The multi-hop case sets `required_source_count: 2`. The evaluator therefore
uses the rank at which both required evidence sources have appeared. This
measures multi-source retrieval only; answer synthesis and claim correctness
need separate answer-level evaluation.

## Run locally

Start AgentFlow in one terminal:

```bash
make dev
```

Seed the corpus and run the Dataset from another terminal:

```bash
make golden-eval
```

For a non-default API port, set `AGENTFLOW_API_BASE_URL`, for example
`AGENTFLOW_API_BASE_URL=http://127.0.0.1:18080 make golden-eval`.

The runner is idempotent by `source_uri`: files already indexed with the same
name are skipped. Use a clean local data store when validating a changed corpus,
because immutable corpus version management belongs to RAG-008.

The command reports every Case as `PASS` or `MISS`. Diagnostic cases are labeled
separately. To return a non-zero exit code when a gating Case misses, run:

```bash
node scripts/run-golden-dataset-v1.mjs --enforce
```

The same Dataset can be pasted into **Knowledge -> Retrieval evaluation** in the
workbench after the corpus files have been indexed. The text area expects the
Dataset object itself; the frontend wraps it in the API request.

## Interpretation

- Hit@K uses answerable Cases as its denominator. A multi-source Case is a hit
  at the rank where its configured number of expected sources has been found.
- A no-answer Case passes only when the Relevance Gate returns no result.
- A forbidden source anywhere in returned Top-K fails the Case.
- `blocked_candidates` is supporting evidence that the prompt-injection guard
  removed hostile material; the injection Case also forbids that source from
  the accepted results.
- Evaluation measures the retrieval pipeline against this corpus. Runtime
  Verification evaluates an Agent Run's configured completion contract; the two
  subsystems are intentionally independent.

## Version discipline

Do not silently edit a published baseline. Changes to Case meaning, expected
sources, or corpus content require a new Dataset and corpus version. RAG-008
will enforce immutable storage and maintain a changelog; until then, Git history
is the version record.
