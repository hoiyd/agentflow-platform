# Evaluation Guide

Evaluation is a separate evidence path, not a Run completion policy. Runtime
[Verification](../runtime/verification.md) decides whether a particular Run may
complete; these suites measure regressions, compatibility, and controlled
comparisons across Runs or code revisions.

| Evidence | Entry point | Scope |
| --- | --- | --- |
| Context, RAG, routing, and Tool quality | [`cmd/eval`](../../apps/api/cmd/eval/main.go), [offline evaluation](offline-evaluation.md) | Versioned datasets, explicit gates, and opt-in budgeted live checks. |
| RAG corpus and Tool tasks | [RAG dataset](rag-golden-dataset.md), [Tool tasks](tool-task-evaluations.md) | Reproducible cases and source-backed outcomes. |
| Local model protocol and token counts | [llama.cpp compatibility](local-inference-compatibility.md), [tokenization calibration](tokenization-calibration.md) | Live target evidence; neither changes production routing or Context limits. |
| Bounded concurrency | [Load and soak](load-soak-testing.md) | Controlled process-local load, not a model-capacity claim. |
| Two-Run comparison | [Evidence comparison](evidence-comparison.md) | Read-only UI for controlled pairs; unrelated Runs remain review-only. |
| Release recovery | [Recovery drill](../operations/release-recovery-drill.md) | Operational procedure with retained evidence; its runbook stays under Operations. |

Go evaluation runners and report envelopes live in
[`internal/evaluation`](../../apps/api/internal/evaluation). The CLI is
[`cmd/eval`](../../apps/api/cmd/eval); reproducible evidence scripts are in
[`scripts/evaluation`](../../scripts/evaluation), exposed through the Makefile.
The production RAG evaluation endpoint remains with the
[knowledge/retrieval owner](../../apps/api/internal/rag/evaluation.go), while
its offline regression runner lives in `internal/evaluation/rageval`.

Offline fixture results, live-model measurements, load tests, and operational
drills answer different questions. Keep their configuration and provenance
attached to each report; do not treat one as evidence for another.
