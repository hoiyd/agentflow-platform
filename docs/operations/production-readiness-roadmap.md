# Deployment Scope and Remaining Work

AgentFlow supports a **trusted, single-instance, Workspace-scoped deployment**.
The controls below are implemented; broader release guarantees require separate
deployment evidence. This is not a claim of general production readiness.

## Current Deployment Profile

| Area | Implemented | Remaining boundary |
| --- | --- | --- |
| Access | Optional [OIDC/Membership](identity-membership.md), owner Workspaces, scoped resources and current revocation checks | Local mode is unauthenticated; team/object sharing, scoped credentials and broader tenant release validation remain separate |
| Execution | Shared modes, admission, Conversation writer, request limits, Run Budget | Process-local ownership; no durable distributed dispatch/lease/fencing |
| Persistence/recovery | Postgres, Stage checkpoints, interrupted lifecycle repair, explicit Resume | Backup/restore, schema rollback and multi-worker takeover are not established by the restart drill |
| Tools/Skills | Guarded Executor, grants, trusted packages, [scoped reads](../tools/scoped-filesystem.md), [managed writes](../tools/scoped-filesystem-writes.md), opt-in [scratch sandbox](sandbox-execution.md) | No arbitrary host code, automatic Skill-script execution or complete durable high-risk approval workflow |
| Evidence | Replay, Ledger, optional [Verification](../runtime/verification.md), [OTel](opentelemetry.md), versioned evaluation | Broader domain calibration, retention/SLO operations and asynchronous human/model grading are not complete |

Browser disconnect already leaves admitted work running and reconnects by durable
sequence. This is not process-crash survival or cross-worker takeover. Committed
effects have receipts; uncertain external outcomes require reconciliation,
not an exactly-once guarantee.

## Next Hardening Priorities

1. **Protect the actual exposure.** Enable existing OIDC/resource checks for
   authenticated use; retain operator-owned credentials and Tool ceilings.
   Add sharing or scoped credentials only with explicit ownership/revocation tests.
2. **Operate retained data safely.** Define deletion/retention for Messages,
   reasoning, Capture, Memory, Artifacts and backups. Rehearse backup/restore and
   version rollback; startup migration alone is not that evidence.
3. **Measure quality and capacity.** Extend representative Golden holdouts,
   retain component identity/failures, set actionable SLOs and tune one control
   at a time. Fixture success is not live quality or inference capacity.
4. **Add durable Workers only when needed.** Atomic dispatch, claim/lease/fencing,
   distributed Conversation ownership and takeover drills must precede a
   multi-instance recovery claim. Do not replace the existing controllers first.

## Deployment Milestones

| Exposure | Required evidence |
| --- | --- |
| Controlled pilot | Deliberate identity boundary, Postgres, retention, backups, telemetry and measured capacity |
| Multi-owner deployment | Existing Membership/resource/Agent/Tool checks plus cross-owner gates, scoped secrets and audit/retention review |
| Distributed Workers | Durable dispatch, stale-owner fencing, checkpoint ownership and termination/takeover tests |
| Enterprise/regional deployment | Contract-driven identity, audit/compliance, recovery and data-residency controls; not a prerequisite for a demo |

## Evidence Standard

Use [functional gates](functional-regression-testing.md),
[load/soak](../evaluation/load-soak-testing.md) and the
[release/recovery drill](release-recovery-drill.md) for their declared scope.
Keep commands, inputs/configuration, revision, failures and retained reports.
Extend cross-owner tests whenever adding a resource or background consumer.

Planned capabilities are not implemented merely because an interface exists.
Budget bounds resource use; Verification checks one configured result; Replay
explains execution; tests/evaluation/drills establish their respective boundaries.
