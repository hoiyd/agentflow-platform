# Documentation

Read the short path first; use the reference sections only for the subsystem
you are changing or investigating.

## Read First

| Goal | Read |
| --- | --- |
| Understand the project | [Overview and demo recording](../README.md) |
| Run and demonstrate it | [Local setup](guides/local-setup.md) -> [Five-minute demo](guides/demo.md) |
| Review the engineering | [Architecture and code map](architecture/backend-architecture.md) -> [Design decisions](architecture/engineering-decisions.md) |
| Check the evidence | [Evaluation guide](evaluation/README.md); fixture passes and live measurements are separate |
| Assess deployment scope | [Status and limits](../README.md#status-and-limits) -> [Production boundaries](operations/production-readiness-roadmap.md) |

## Technical Reading Paths

- **Runtime:** [modes](runtime/execution-modes.md) -> [control ownership](runtime/execution-controls.md) -> [recovery](runtime/durable-recovery.md).
- **RAG:** [pipeline](knowledge/knowledge-rag.md) -> [Golden Dataset](evaluation/rag-golden-dataset.md) -> [reranking experiment](knowledge/cross-encoder-reranking.md).
- **Tools:** [security contract](tools/tool-security-policy.md) -> [multi-round loop](tools/bounded-tool-loop.md) -> [uncertain effects](tools/tool-side-effect-reconciliation.md).
- **Context:** [assembly and compaction](context/context-management.md) -> [Task State](runtime/task-state.md) -> [live fidelity experiment](evaluation/live-compaction.md).
- **Outcome:** [Verification](runtime/verification.md) -> [structured results](runtime/structured-output.md) -> [controlled comparison](evaluation/evidence-comparison.md).

## Reference

These are implementation and operating contracts, not a required reading list.

<details>
<summary>Architecture, API, and frontend</summary>

- [Terms and Runtime Snapshot](architecture/terms.md)
- [Storage](architecture/storage-boundary.md), [API change workflow](architecture/api-contract.md), [HTTP reference](reference/api-reference.md), [OpenAPI](../api/openapi.yaml)
- [Workspace Agent/Tool ownership](architecture/workspace-agent-tools.md)
- [Frontend principles](architecture/frontend-experience.md), [CSS organization](../apps/web/app/styles/README.md)

</details>

<details>
<summary>Runtime, context, and recovery</summary>

- [Agent profiles](runtime/agent-profiles.md), [selection](runtime/agent-selection.md), [model routes](runtime/model-routing.md), [provider reasoning](runtime/provider-reasoning.md)
- [Run Budget / Usage Ledger](runtime/run-budget.md), [read-only planning](runtime/read-only-planning.md)
- [Memory](context/memory-management.md), [request reconstruction / Capture](context/model-request-reconstruction.md)
- [Clarification](runtime/structured-clarification.md), [durable steering](runtime/durable-inputs.md), [partial output](runtime/durable-partial-output.md)
- [Event catalog](runtime/event-catalog.md), [projections/invariants](runtime/event-projections-runtime-invariants.md), [failures](runtime/failure-handling.md), [operator attention](runtime/operator-attention.md)

</details>

<details>
<summary>Knowledge and metadata</summary>

- [Equality filters](knowledge/metadata-filters.md), [typed filters](knowledge/typed-metadata-filters.md), [Runtime scope](knowledge/runtime-knowledge-filters.md)
- [Metadata schema](knowledge/metadata-schema.md), [metadata updates](knowledge/metadata-updates.md), [document lifecycle](knowledge/document-lifecycle.md)

</details>

<details>
<summary>Tools and Skills</summary>

- [Discovery](tools/tool-discovery.md), [result Artifacts](tools/tool-result-artifacts.md), [Progress Guard](tools/tool-progress-guard.md), [execution progress](tools/tool-execution-progress.md)
- [Knowledge bindings](tools/scoped-knowledge-tools.md), [Web citations](tools/web-source-citations.md)
- [Scoped filesystem reads](tools/scoped-filesystem.md), [managed writes](tools/scoped-filesystem-writes.md)
- [Trusted Skills](tools/trusted-skills.md), [invocation/readiness](tools/skill-invocation.md), [installation](tools/skill-installation.md), [Checker](tools/skill-checker.md)
- [Binding contracts and fault tests](tools/tool-contract-testing.md)

</details>

<details>
<summary>Configuration, security, and operations</summary>

- [Backend configuration](operations/backend-configuration.md), [credentials/redaction](operations/credential-boundary.md), [OpenTelemetry](operations/opentelemetry.md)
- [Identity/Membership](operations/identity-membership.md), [Workspace lifecycle](operations/workspace-lifecycle.md), [resource authorization](operations/resource-authorization.md)
- [Instruction/data boundaries](operations/agent-security.md), [network/host execution](operations/execution-boundaries.md), [sandbox commands](operations/sandbox-execution.md)
- [Browser functional gates](operations/functional-regression-testing.md), [manual tests](operations/manual-tests.md), [release/recovery drill](operations/release-recovery-drill.md)

</details>

<details>
<summary>Evaluation procedures and retained results</summary>

- [Offline gates and benchmark pack](evaluation/offline-evaluation.md), [failure corpus](evaluation/failure-corpus.md)
- [Tool tasks](evaluation/tool-task-evaluations.md), [Golden Dataset schema](schemas/rag-golden-dataset-v1.schema.json)
- [llama.cpp compatibility](evaluation/local-inference-compatibility.md), [tokenization calibration](evaluation/tokenization-calibration.md), [load/soak](evaluation/load-soak-testing.md)

</details>

## Documentation Ownership

Keep the README introductory, this page navigational, and each contract in one
owning document. Defaults belong in [`.env.example`](../apps/api/.env.example),
DTOs in OpenAPI, and event names in the generated catalog. Preserve executable
steps, failure semantics, evidence provenance, and limitations when editing.
Do not turn implementation notes or private backlogs into the public reading path.
