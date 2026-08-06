# Production Readiness Roadmap

AgentFlow is an inspectable AI runtime project; the repository does not claim
that it is already production-ready. This roadmap separates the controls
already implemented from the work required before the platform should accept
real multi-tenant traffic.

The target MVP is a **single-region, multi-workspace, controlled beta**. It uses
Postgres as the production state store, runs only approved and bounded Tools,
and has measured quality, recovery, and operational gates. Multi-region
active-active, arbitrary code execution, and enterprise compliance packages are
deliberately outside that MVP.

## Implemented Baseline

The roadmap builds on working contracts rather than replacing them:

| Area | Current evidence |
| --- | --- |
| Execution | [Single, Multi, and Loop](execution-modes.md) share one Turn Engine and frozen Runtime Snapshot. |
| Resource control | [Run admission, backpressure, rate limits, retries, budgets, and timeouts](execution-controls.md) have distinct scopes and enforcement owners. |
| Persistence | File and Postgres stores persist Runs, Messages, Run Events, usage, retrieval data, and Verification records behind common domain contracts. |
| Tool execution | [Platform enablement and Agent allowlists](agent-profiles.md#two-tool-control-layers) precede a bounded Executor with typed errors, timeout, result limits, tracing, and conservative concurrency. |
| RAG | [Hybrid recall, RRF, reranking, relevance gating, scoped context expansion, injection filtering, and citations](knowledge-rag.md) use one observable pipeline. |
| Evaluation | The [retrieval evaluation API](api-reference.md#rag-evaluation) reports Hit@1/3/5, misses, security decisions, and active component versions. |
| Evidence | [Typed Run Events, Replay, Episode Reports](terms.md#observability-records-and-views), Usage Ledger, and [Verification Evidence](verification.md) explain execution and configured outcome checks. |

These controls make failures bounded and inspectable. They do not by themselves
provide identity, mandatory tenant isolation, distributed ownership, calibrated
quality thresholds, or production operations.

## Current Production Boundaries

1. **Identity and isolation:** the API has no built-in authentication or
   authorization layer. Workspace filtering exists in selected paths but is not
   yet a mandatory, fail-closed invariant across every Store and background job.
2. **Execution ownership:** Run admission, queueing, and Conversation
   single-writer control are process-local. Startup recovery can identify stale
   Runs, but there is no distributed lease, fencing token, or durable dispatch
   protocol.
3. **Tool security:** current built-in Tools are bounded, but Resource Scope,
   Credential Scope, outbound network policy, side-effect classification, and a
   general Sandbox are not complete.
4. **Quality gates:** retrieval cases are repeatable, but the project does not
   yet maintain a versioned Golden Dataset with calibrated release thresholds
   and grounded-answer evaluation.
5. **Operations:** typed events support inspection, but standard telemetry,
   SLOs, alerting, backup/restore drills, canary rollout, and measured capacity
   limits remain production work.

## MVP Phase 1: Secure Data Plane

The first phase makes tenant and Tool boundaries enforceable before adding more
external capabilities.

- Establish authenticated User, Workspace, and Membership identities. Derive a
  trusted Runtime Scope for HTTP, SSE, Worker, Resume, and cleanup paths instead
  of trusting a client-supplied `workspace_id`.
- Make `workspace_id` non-optional in production data. Require scoped Store
  operations for Conversations, Runs, Agents, Documents, chunks, Memory,
  Artifacts, Replay, Episode Reports, Evaluation, and Verification.
- Enforce tenant filters inside database and retrieval queries. Cross-workspace,
  missing-scope, forged-ID, parent-expansion, and background-job cases become
  release-blocking tests.
- Resolve Provider and Tool credentials through scoped references. Apply one
  versioned redaction policy before data reaches Snapshots, Events, Artifacts,
  logs, or telemetry; resolution and redaction failures fail closed.
- Extend Tool policy with Resource Scope, Credential Scope, Network Policy,
  Side-effect Class, and Approval Policy. The MVP permits only reviewed,
  bounded, read-only Tools and exact outbound targets.
- Define retention and deletion for Messages, Run Events, Artifacts, Memory,
  captures, and evaluation data. Record append-only security decisions without
  persisting sensitive payloads in the audit trail.

**Phase gate:** no request, search, expansion, Replay, Resume, or background job
can cross Workspace scope; test secrets do not appear in persisted or exported
data; write-capable, arbitrary-code, and unauthorized-network Tools are denied
before their handlers run.

## MVP Phase 2: Durable Execution

The second phase moves ownership from an API process to a recoverable execution
protocol.

- Require Postgres in production and adopt versioned, locked, backward-compatible
  migrations. Exercise backup and restore instead of treating backup
  configuration as evidence of recovery.
- Persist Run dispatch atomically with Run creation. Bounded Workers claim jobs
  with stable operation IDs, explicit attempts, backpressure, and idempotent
  usage reservation and settlement.
- Decouple client event delivery from Worker lifetime. SSE reconnection resumes
  from a durable event cursor; stream-only delta loss is explicit, and the
  persisted final Message remains authoritative.
- Add owner, lease expiry, heartbeat, recovery cursor, and fencing token to the
  runtime Session. Every state commit rejects a stale owner, including a Worker
  that resumes after losing its lease.
- Promote Conversation single-writer control from an in-process lock to a
  distributed ownership rule.
- Persist Stage checkpoints with input/output hashes, Runtime Snapshot reference,
  Event cursor, and commit status. Resume skips committed internal work and
  never silently converts an uncertain side effect into a retry.
- Define readiness, graceful shutdown, takeover, and terminal-state behavior.
  Exercise database loss, Worker crash, slow/failed Provider calls, rolling
  deployment, and recovery from each Stage boundary.

**Phase gate:** killing an API or Worker does not lose an admitted Run; lease
takeover cannot produce two valid writers; committed Stages, usage, and Messages
are not duplicated; backup restore and rolling deployment have recorded drill
results.

## MVP Phase 3: Evidence-Based Operations

The third phase turns existing execution evidence into release and operational
decisions.

- Store a versioned Golden Dataset covering facts, paraphrases, exact IDs,
  no-answer cases, stale sources, tenant ACLs, and prompt-injection attempts.
  Bind results to corpus, embedding, fusion, reranker, gate, Prompt, and model
  versions.
- Gate releases on retrieval quality and security outcomes. Any cross-workspace
  or forbidden-source hit blocks release; subjective answer quality initially
  combines deterministic checks with sampled human review rather than an
  uncalibrated LLM judge.
- Project typed Run Events into OpenTelemetry traces and bounded metrics without
  making telemetry a second business source of truth. Correlate request, Run,
  Workspace, operation, Worker, Provider, and database activity.
- Define user-visible SLOs for API availability, Run start delay, queue wait,
  and terminal/recoverable outcomes. Alerts identify the ownership layer and
  link to tested Runbooks.
- Use immutable artifacts, versioned configuration, Feature Flags, Workspace
  canaries, backward-compatible migrations, and rehearsed rollback for code,
  Prompt, model, and retrieval changes.
- Run load and soak tests across SSE, queues, Workers, Postgres, retrieval, and
  Provider stubs. Publish only measured capacity, saturation, and recovery
  behavior for the tested environment.

**Phase gate:** the quality baseline is reproducible, security cases are clean,
critical failures produce actionable alerts, a migration/canary rollback has
been rehearsed, and overload degrades through bounded queueing or rejection
without sustained resource leakage.

## After the MVP

The following work expands the supported risk and scale envelope; it is not
required for the controlled beta:

- **Sandbox and side effects:** isolate code, Shell, filesystem, and external
  writes; add idempotency keys, approval, Outbox, reconciliation, and bounded
  compensation without claiming every external action is reversible.
- **External Tool sources:** introduce stable source identity, Secret references,
  lifecycle management, schema validation, frozen definitions, Resume checks,
  and failure harnesses before enabling curated MCP Tools. MCP annotations are
  untrusted hints, and every call must still pass through the existing Catalog,
  policy, Budget, Executor, and Event contracts.
- **Skills:** treat Skills as governed, versioned content with provenance and
  policy. Do not concatenate arbitrary external instructions into the system
  Prompt or allow a Skill to widen Tool, Credential, network, or budget scope.
- **Online evaluation:** add shadow and canary evaluation for model, Prompt, and
  retrieval changes after offline metrics are calibrated.
- **Provider resilience and cost:** add capability-aware routing, circuit
  breakers, fallback, per-Workspace quotas, and cost/quality monitoring.
- **Enterprise and regional controls:** add SSO/SCIM, fine-grained RBAC, SIEM
  export, legal retention workflows, active-passive disaster recovery, and only
  then evaluate multi-region active-active or tenant-managed keys.

## Go-Live Standard

AgentFlow should be described as ready for a controlled production beta only
when all three MVP phase gates pass. Run Budget, Verification, Trace, and tests
remain complementary evidence:

- Budget limits resource use.
- Verification checks configured invariants for one Run.
- Trace and Replay explain what happened.
- Tests, evaluation, and failure drills show that system boundaries remain true
  under change and failure.

Plans are not implementation evidence. Until a roadmap item is implemented,
tested, and linked from the main engineering evidence table, it remains a
declared boundary rather than a platform capability.
