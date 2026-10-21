# Failure Corpus and Regression Promotion

This is a provenance index over existing owned regressions, not another test
runner or an Agent quality score. A failure moves through **candidate -> human
review -> promotion -> existing regression -> retained gate report**.

## Review and Promote

From `apps/api`, generate a pending candidate from an operator-retained Replay
or supported evaluation report:

```bash
go run ./cmd/eval failures candidate --artifact /tmp/failure-source.json \
  --id failure-id --owner provider \
  --source-ref https://example.com/issues/failure-id \
  --source-revision FULL_GIT_SHA \
  --evidence-ref https://example.com/retained/failure-source.json \
  > /tmp/failure-candidate.json
```

Generation records only the artifact kind and SHA-256 plus explicitly supplied
provenance. It never copies prompts, errors, Memory, Tool results, credentials,
or source paths. It does not decide whether an observed Run is a real failure.
Supported sources are Replay and the current Context, live compaction, RAG,
routing and Tool evaluation schemas; other formats require a reviewed manual
manifest. Raw artifacts remain private; evidence may reference access-controlled
storage. References must be HTTPS without credentials, query or fragment.

A human checks the evidence and fills `failure_class`, `input_boundary`,
`expected_outcome`, and one exact Go `regression.package`/`regression.test` in
the owning suite. Set `review.status=approved`, reviewer, UTC `reviewed_at`,
review notes and `review.redaction=confirmed`. Review refers to this entire
manifest, including its summary and references; re-review changed content.
Approval fields are auditable Git assertions, not authenticated signatures.

```bash
go run ./cmd/eval failures promote --candidate /tmp/failure-candidate.json \
  --corpus ../../examples/failures/corpus.v1.json > /tmp/promoted-corpus.json
```

Inspect the output, then replace the corpus through the normal Git review flow.
Do not redirect over the input file. Promotion never modifies input files:
identical repeats produce identical sorted JSON, conflicting IDs are rejected.
Every content change alters the report's canonical Dataset hash. Unsupported
schema, unknown owner, absent evidence, pending review or redaction are rejected.

## Gate and Evidence

Run the mapped tests with `-count=1`; no test command is executed from a manifest.
For the initial three historical candidates:

```bash
revision=$(git rev-parse HEAD)
go test -json ./internal/agent/toolloop ./app -count=1 \
  -run '^(TestMultiRoundPreservesReasoningWithoutExposingIt|TestMultiRoundTaskStateWriteUsesRealStageOrTurnIdentity|TestObservableWorkspacePreservesArtifactReads)$' \
  > /tmp/failure-tests.json
go run ./cmd/eval failures check --results /tmp/failure-tests.json \
  --results-revision "$revision" --enforce > /tmp/failure-regressions.json
```

Postgres prerequisites are the same as the [functional gates](../operations/functional-regression-testing.md#run-and-evidence).
A missing test database produces a skipped Artifact regression, **not** a green
corpus gate. Use a dedicated disposable test server, never the application DB.

The report reuses `agentflow-evaluation-report-v1` with a separate
`failure-regression-report-v1` payload: Dataset hash, current/source revisions,
exact regression targets, results SHA-256, per-case status and gate. Keep the
original `go test -json` artifact and command with it. Results revision is
caller-declared and must match checkout HEAD; this is provenance, not proof
against fabricated or mislabeled test output. Dirty-tree evidence must be
identified as such; rerun after committing for an immutable revision.

Every promoted case remains in the denominator. Root or child failure, skip,
missing target or incomplete package results block the gate. `--enforce` exits
1 for a nonpassing gate; invalid inputs/revision exit 2. Test log text is not
copied into the report. CI consumes the ordinary backend test JSON and retains
both artifacts; no additional runtime, network calls, or live model budget.

## Historical Seeds and Limits

The [corpus](../../examples/failures/corpus.v1.json) starts with three historical
technical summaries, explicitly approved by the repository owner on 2026-10-21:
#151 Provider continuation, #152 internal Task State write, and #167 Workspace
Artifact capability loss. They reference immutable public fix code and SHA-256,
not raw incident captures. The initial gate passed all three owned regressions
against disposable Postgres. Pending candidates and an empty corpus cannot pass.

New samples stay with their RAG, Context, Tool, routing or recovery owner; this
index records the existing gate target rather than moving all tests. The first
adapter consumes Go test JSON only. Browser/live/drill artifacts can motivate a
candidate, but need a mapped owned Go regression here; their original suites
remain independently authoritative. No online collection, auto-labeling,
automatic approval, new dashboard, universal score or baseline comparison is
introduced. Domain comparisons keep their existing identity and denominator
rules. Source reachability, source hashes and redaction require human review;
the offline gate does not fetch private evidence or attest its authenticity.
