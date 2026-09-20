# Release and Recovery Drill

PROD-014 turns AgentFlow's existing recovery contracts into repeatable
single-instance process evidence. It does not add a deployment platform or a
second recovery implementation.

## Scope

The drill builds the current API revision, starts real API child processes
against PostgreSQL, and drives them only through public HTTP endpoints. A local
OpenAI-compatible fixture keeps the exercise deterministic and free of provider
cost; it proves process and persistence behavior, not model quality.

| Step | Fault or boundary | Required evidence |
| --- | --- | --- |
| Startup dependency | Start without DATABASE_URL | Process exits before readiness with an explicit dependency error |
| Migration and readiness | Start against PostgreSQL, then call GET /health | Startup migration and schema validation finish before HTTP 200 |
| In-flight drain | Send SIGTERM while a model request is active | The accepted Run completes and the process exits cleanly |
| Restart read | Start a new API process | The completed Run remains readable through Replay |
| Worker crash | Send SIGKILL while a checkpointed Stage is active | Durable Run and Stage evidence survive abrupt termination |
| Stale repair | Start a new API process after heartbeat expiry | The Run becomes failed_recoverable with repaired terminal events |
| Resume | Call POST /api/runs/{id}/resume | The interrupted Stage is compensated and the same Run completes |
| Duplicate Resume | Repeat the Resume call | HTTP 409 prevents duplicate execution |
| Uncertain Tool effect | Run the focused recovery contract test | An unresolved external effect blocks Resume |

The health endpoint acts as process readiness in this drill because the API does
not start listening until PostgreSQL connection, idempotent migrations, schema
validation, recovery scanning, and dependency construction have succeeded.

## Run

Use a disposable PostgreSQL database with pgvector. The drill creates durable
records intentionally and does not clean them up, because their IDs are part of
the retained evidence.

```bash
RELEASE_DRILL_DATABASE_URL='postgres://...' make release-recovery-drill
```

TEST_DATABASE_URL is used when RELEASE_DRILL_DATABASE_URL is absent. The script
refuses to fall back to the normal application DATABASE_URL. The orchestration
lives in an opt-in integration test rather than the production server or CLI;
ordinary `go test ./...` runs skip it unless the release-drill environment is
present.

The default evidence path is:

```text
.cache/release-drill/latest.json
```

The report records the Git revision, whether the public working tree was clean,
Go version, expected and observed result, duration, Run IDs, checkpoint states,
event types, and any failed step. Database URLs and credential values are
neither logged nor persisted. Partial evidence is written after every step, so
a failed drill remains diagnosable. Release evidence should be captured from a
clean committed tree; development runs remain valid diagnostics but report
working_tree_clean as false.

## Passing Contract

A passing report has schema version agentflow-release-recovery-drill-v1,
status passed, and every item in steps marked passed. The recovered Replay must
contain run.failed, stage.failed, run.resumed,
checkpoint.compensation_completed, and run.completed across the repair and
Resume phases.

The fixture-backed uncertain-effect check is labeled automated_contract_test;
it validates the fail-closed policy but is not presented as a real external
write.

## Deliberate Limits

This local drill is evidence for single-process lifecycle recovery. It is not a
claim of production release readiness, zero-downtime deployment, distributed
takeover, or exactly-once external effects.

The report explicitly leaves these deployment-owned exercises deferred:

- backup into a separate target and restore verification;
- reading new data with a retained previous release binary;
- migration rollback policy.

Those require a real release artifact, backup destination, version matrix, and
deployment runbook. Multi-worker recovery additionally requires durable
dispatch, lease, heartbeat, and fencing rather than extending this script.
