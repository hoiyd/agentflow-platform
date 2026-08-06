# Production Readiness Roadmap

AgentFlow already implements production-oriented controls for bounded
execution, persistence, observability, and outcome verification. This roadmap
defines the deployment scope those controls support today and how that scope can
expand safely from a trusted single-workspace environment to multi-workspace
and distributed operation.

Production readiness is treated as a deployment-specific claim, not a binary
label. Each milestone below has a narrower risk envelope than the next; features
needed only for enterprise or regional scale are not presented as blockers for
a controlled pilot.

## Production-Oriented Baseline

| Area | Implemented control and evidence |
| --- | --- |
| Execution | [Single, Multi, and Loop](execution-modes.md) share one Turn Engine, frozen Runtime Snapshot, bounded lifecycle, and common completion path. |
| Resource control | [Admission, backpressure, rate limits, retries, context limits, Run Budget, and timeouts](execution-controls.md) have distinct scopes and enforcement owners. |
| Persistence | File and Postgres stores persist Runs, Messages, durable Run Events, usage, retrieval data, and Verification records behind shared domain contracts. |
| Tool execution | [Platform enablement and Agent allowlists](agent-profiles.md#two-tool-control-layers) precede a bounded Executor with typed errors, timeout, result limits, tracing, and conservative concurrency. |
| RAG | [Hybrid recall, RRF, reranking, relevance gating, scoped context expansion, injection filtering, and citations](knowledge-rag.md) use one observable pipeline. |
| Evaluation | The [retrieval evaluation API](api-reference.md#rag-evaluation) reports Hit@1/3/5, misses, security decisions, and active component versions. |
| Evidence | [Run Events, Replay, Episode Reports](terms.md#observability-records-and-views), Usage Ledger, and [Verification Evidence](verification.md) explain execution and configured outcome checks. |

These controls are useful now: they bound work, preserve execution evidence,
and expose failure behavior. The remaining work expands where and at what scale
the system can be operated safely; it does not replace the runtime foundation.

## Current Deployment Profile

The current supported profile is a **trusted, single-workspace deployment** for
evaluation, internal use, or a controlled demonstration:

| Dimension | Current scope |
| --- | --- |
| Access | Deploy behind a trusted network or external access boundary; the API does not yet provide built-in authentication or authorization. |
| Tenancy | Treat the system as single-workspace. Existing Workspace filters improve scoping but are not a complete multi-tenant isolation guarantee. |
| Runtime | Run admission, bounded queueing, Conversation single-writer control, and stale-Run recovery operate within one process. |
| Tools | Use built-in or operator-reviewed Tools. All calls pass through Agent allowlists, Budget, timeout, result limits, tracing, and conservative concurrency. |
| Data | File Store supports local operation; Postgres provides durable storage. Automated backup, restore, and migration drills remain deployment responsibilities. |
| Quality | Retrieval evaluation is repeatable and version-aware. A maintained Golden Dataset and calibrated release thresholds are the next quality step. |

This profile deliberately excludes untrusted public access, hard multi-tenant
claims, arbitrary code execution, and distributed Worker ownership.

## Deployment Milestones

| Milestone | Additional boundary required |
| --- | --- |
| **Controlled single-tenant pilot** | External identity boundary, Postgres, backup/restore drill, retention and redaction policy, baseline telemetry, Runbooks, and measured capacity. |
| **Multi-workspace beta** | Built-in identity and Runtime Scope, mandatory Store/retrieval isolation, scoped Credentials, Tool network/resource policy, and cross-tenant security tests. |
| **Distributed deployment** | Durable dispatch, independent Workers, reconnectable event delivery, Lease/Heartbeat/Fencing, Stage checkpoints, and takeover drills. |
| **Enterprise or regional scale** | SSO/SCIM, fine-grained RBAC, audited write-capable Sandboxes, online evaluation, Provider failover, regional recovery, and contractual compliance controls. |

The first milestone is intentionally smaller than a general multi-tenant SaaS
launch. Later milestones should be pulled forward only when the deployment
model or workload requires them.

## Next Hardening Priorities

### 1. Secure Access and Data Boundaries

- Derive authenticated User, Workspace, and Membership scope at the request
  boundary; do not trust a client-selected `workspace_id`.
- Make production Store, RAG, Memory, Replay, Verification, and background-job
  operations fail closed when required scope is absent.
- Resolve Provider and Tool secrets through scoped references, and apply one
  redaction policy before Events, Artifacts, logs, or telemetry are persisted.
- Add Resource, Credential, Network, Side-effect, and Approval policy ahead of
  Tool execution. Agent allowlists may narrow platform policy, never widen it.
- Define retention, deletion, and append-only security audit behavior without
  placing sensitive Prompt or Tool payloads in audit records.

### 2. Durable Execution and Recovery

- Make Postgres the production state source and adopt versioned,
  backward-compatible migrations with exercised backup and restore.
- Persist Run dispatch atomically with Run creation; bounded Workers claim work
  with stable operation IDs and idempotent usage accounting.
- Decouple SSE delivery from Worker lifetime. Reconnection follows a durable
  event cursor, while the persisted final Message remains authoritative.
- Use Lease, Heartbeat, and Fencing to prevent stale Workers from committing;
  promote Conversation single-writer control from a process lock to distributed
  ownership when multiple Workers are introduced.
- Persist Stage checkpoints and verify recovery through Worker termination,
  dependency failure, rolling deployment, and takeover drills.

### 3. Quality and Operational Evidence

- Maintain a versioned Golden Dataset covering facts, paraphrases, exact IDs,
  no-answer cases, stale sources, tenant ACLs, and injection attempts.
- Bind evaluation results to corpus, embedding, fusion, reranker, relevance
  policy, Prompt, and model versions. Security leakage remains a hard failure.
- Project typed Run Events into OpenTelemetry without making telemetry a second
  business source of truth; bound sampling, cardinality, retention, and content.
- Define SLOs and actionable alerts for admission, queueing, execution,
  Provider, database, recovery, and quality failures.
- Use canary rollout, compatible migrations, rehearsed rollback, and load/soak
  tests. Publish only measured capacity and recovery behavior.

## Scale-Driven Evolution

These capabilities expand the risk or scale envelope and are not prerequisites
for a controlled single-tenant pilot:

- **External Tool sources:** curated MCP Tools require stable source identity,
  Secret references, lifecycle management, schema validation, frozen
  definitions, Resume checks, and failure harnesses. MCP remains a Tool source;
  it does not bypass the existing Catalog, policy, Budget, Executor, or Events.
- **Sandbox and side effects:** code, Shell, filesystem, and external writes
  require isolation, idempotency keys, approval, Outbox/reconciliation, and
  bounded compensation. A timeout alone does not prove that a remote write
  failed.
- **Skills:** govern Skills as versioned content with provenance. A Skill cannot
  widen Tool, Credential, network, or budget scope, or inject arbitrary trusted
  instructions.
- **Online evaluation and Provider resilience:** add calibrated shadow/canary
  evaluation, capability-aware routing, circuit breakers, fallback, and
  per-Workspace cost controls after offline baselines are stable.
- **Enterprise and regional controls:** add SSO/SCIM, SIEM export, legal
  retention, active-passive regional recovery, tenant-managed keys, and only
  then evaluate multi-region active-active operation.

## Evidence Standard

A roadmap item becomes a platform capability only when its implementation,
tests, failure behavior, and operational evidence are linked from the main
engineering evidence table. Plans remain declared direction, not proof.

Run Budget, Verification, Trace, and tests provide complementary evidence:

- Budget limits resource use.
- Verification checks configured invariants for one Run.
- Trace and Replay explain what happened.
- Tests, evaluation, and drills show that system boundaries survive change and
  failure.
