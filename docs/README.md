# Documentation Guide

Start with the question you need to answer. Each topic has one owning document;
related pages link to its contract rather than copying configuration or algorithms.

## Start Here

1. [Project overview](../README.md): capabilities and supported boundaries.
2. [Local setup](guides/local-setup.md): startup and test prerequisites.
3. [Five-minute demo](guides/demo.md): one task from execution to evidence.
4. [Execution modes](runtime/execution-modes.md): Single, Multi, Loop, and disconnect/recovery behavior.
5. [Backend architecture](architecture/backend-architecture.md) and [internal terms](architecture/terms.md): ownership and execution entities.

## Architecture

- [Engineering decisions](architecture/engineering-decisions.md): rationale, trade-offs, and retired approaches.
- [Storage boundary](architecture/storage-boundary.md): Postgres-only persistence versus non-durable test fixtures.
- [API contract](architecture/api-contract.md): shared OpenAPI generation and change workflow.
- [Frontend principles](architecture/frontend-experience.md): desktop workbench constraints and Run Session ownership.
- [Stylesheet organization](../apps/web/app/styles/README.md): CSS ownership.

## Runtime and Operations

- **Orchestration:** [Agent profiles](runtime/agent-profiles.md), [Agent selection](runtime/agent-selection.md), and [execution modes](runtime/execution-modes.md).
- **Limits:** [execution controls](runtime/execution-controls.md) maps scope, units, precedence, and tuning; [Run Budget](runtime/run-budget.md) owns reservation and settlement details.
- **Model access:** [route catalog](runtime/model-routing.md), including sampling, affinity, and Resume.
- **Provider output:** [reasoning display](runtime/provider-reasoning.md), explicit format support, sanitized persistence and historical answer association.
- **Completion:** [Verification](runtime/verification.md), distinct from development tests and offline evaluation.
- **Recovery:** [checkpoints and actions](runtime/durable-recovery.md), [partial output recovery](runtime/durable-partial-output.md), [operator attention](runtime/operator-attention.md), and [release/recovery drill](operations/release-recovery-drill.md).
- **Events:** [generated catalog](runtime/event-catalog.md), [projections/invariants](runtime/event-projections-runtime-invariants.md), and [failure contracts](runtime/failure-handling.md).
- **Configuration:** [backend settings](operations/backend-configuration.md), [credential/redaction boundary](operations/credential-boundary.md), and [production readiness](operations/production-readiness-roadmap.md).
- **Access:** [OIDC identity and Workspace membership](operations/identity-membership.md): AgentFlow authentication theme, personal Workspace onboarding, database grants, revocable sessions and remaining authorization boundaries.
- **Workspaces:** [owner-scoped lifecycle](operations/workspace-lifecycle.md): generated IDs, default selection, archive/soft deletion and current-schema initialization.
- **Authorization:** [resource boundaries](operations/resource-authorization.md): object ownership, linked evidence, nested resources, live-stream revocation and repeatable cross-owner tests.
- **Execution boundary:** [governed egress and host execution](operations/execution-boundaries.md): destination enforcement, trusted-local command limits, filesystem ownership and failure evidence; distinct from an OS sandbox.
- **Isolated commands:** [sbx execution](operations/sandbox-execution.md): disposable mountless VMs, deny-all network, resource limits, command receipts and restart cleanup.
- **Interfaces:** [HTTP API reference](reference/api-reference.md) maps routes and operational semantics; [OpenAPI](../api/openapi.yaml) owns DTO shapes.

## Context and Knowledge

- [Context management](context/context-management.md): selection, exact compaction algorithm, ratios, and failure behavior.
- [Durable inputs](runtime/durable-inputs.md): safe steering boundaries, queued follow-up Runs, withdrawal, and recovery semantics.
- [Model request reconstruction](context/model-request-reconstruction.md): actual attempt payloads, opt-in capture, inference telemetry, and inspection.
- [Memory management](context/memory-management.md): recall, proposal, sync, trust, corrections, and deletion.
- [Structured Task State](runtime/task-state.md): versioned facts across Runs, independent of summaries.
- [Knowledge / RAG](knowledge/knowledge-rag.md): index lifecycle, recall/ranking/Gate, context expansion, injection filtering, and native citations.
- [Cross-Encoder reranking](knowledge/cross-encoder-reranking.md): optional TEI service, bounded requests, privacy boundary, and same-candidate ablation.

## Tools and Skills

- [Security policy](tools/tool-security-policy.md): capabilities, derived journal boundary, scope, credentials, and new-Tool registration.
- [Agent instruction/data boundaries](operations/agent-security.md): fixed platform guidance, private-data Tool egress, approval semantics and attack regressions.
- [Bounded Tool loop](tools/bounded-tool-loop.md): model/Tool rounds, complete continuation protocol, streaming, and recovery limits.
- [Result Artifacts](tools/tool-result-artifacts.md), [Progress Guard](tools/tool-progress-guard.md), and [side-effect reconciliation](tools/tool-side-effect-reconciliation.md).
- [Tool execution progress](tools/tool-execution-progress.md): bounded semantic updates, committed recovery, shared Chat/Replay display and Binding integration.
- [Scoped Knowledge bindings](tools/scoped-knowledge-tools.md) and [Web citations](tools/web-source-citations.md).
- [Trusted Skills](tools/trusted-skills.md): Loader contract, Agent binding, progressive activation, frozen content, and resource limits.
- [Skill installation](tools/skill-installation.md): native Vercel workflow, root checks, restricted Go fallback, provenance, and publication safety.
- [Skill Checker](tools/skill-checker.md): read-only compatibility diagnostics, resource references, Tool dependency checks, and unsupported semantics.
- [Tool contract/fault testing](tools/tool-contract-testing.md): shared Binding suite and adding coverage for a new Tool.

## Validation and Evidence

Start with the [evaluation guide](evaluation/README.md) to choose the correct evidence path:

- [Offline regression reports](evaluation/offline-evaluation.md): Context, routing, RAG, Tool, and budgeted live benchmark profiles.
- [RAG dataset](evaluation/rag-golden-dataset.md) and [JSON Schema](schemas/rag-golden-dataset-v1.schema.json); [Tool task evaluations](evaluation/tool-task-evaluations.md).
- [llama.cpp compatibility](evaluation/local-inference-compatibility.md) and [tokenization calibration](evaluation/tokenization-calibration.md).
- [Bounded load/soak](evaluation/load-soak-testing.md) and [controlled Run comparison](evaluation/evidence-comparison.md).
- [Functional regression gates](operations/functional-regression-testing.md): browser -> Go -> Postgres checks with retained runtime evidence.
- [Manual tests](operations/manual-tests.md): observable checks performed by an operator.

Fixture evidence, live model measurements, runtime Verification, and recovery
drills answer different questions. Keep configuration, provenance, failures,
and limitations with each result; a successful trace is not a quality benchmark.

## Maintenance Rules

- Keep this index navigational and the project README introductory.
- Defaults belong in [`.env.example`](../apps/api/.env.example); API shapes in
  [OpenAPI](../api/openapi.yaml); event names in the generated catalog. Explain
  non-obvious semantics in the owning topic rather than maintaining copies.
- Keep executable setup/demo commands and safety limits, even when trimming prose.
- Update references when moving or merging a page. Keep backlog/archive and
  private notes separate from supported product documentation.
- When code and docs disagree, verify the implementation/tests and update the
  owner. Distinguish current behavior from historical migrations and planned work.
