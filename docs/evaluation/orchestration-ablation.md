# Orchestration Mode Ablation

`eval modes` measures whether Single, Multi-Agent or bounded Loop is worth its
extra orchestration on one sourced-report task. It runs the production application,
HTTP commands, scoped `file_read`, and Postgres persistence, not three prompt-only
simulations. It does not change [Run Comparison eligibility](evidence-comparison.md#comparability).

## Controlled Task

The [versioned dataset](../../examples/orchestration/report-task.v1.json) contains
one synthetic release brief, six expected JSON fields, an exact quotation and a
failure inventory. Every arm gets a fresh Conversation/Run, the same Agent,
source file, model, temperature (`0`), context configuration and per-Run ceilings.
The fixed route has a 32,768-token context window and 2,048-token output limit.
The sensor checks the persisted assistant answer, authorized file-read receipt
and source hash, rejects extra output fields, then verifies that the source file
has not changed.

Only orchestration is the declared experiment axis: `single`, `multi_agent`,
`autonomous`. Mode prompts, stage sequence, built-in control-tool availability,
approval and Loop limits form that bundle; final model requests are **not**
identical. The evaluator compares the remaining frozen Runtime Snapshot and
actual Worker identity before emitting deltas. Model, sampling, Agent, Tool
definition, material or budget drift disables comparison.

Memory/Knowledge start empty; auxiliary-stage embedding calls use a local fixed
transport, not an external embedding model. Production built-in control/read
tools remain registered; this task accepts only `file_read` operations as evidence.
It does not benchmark RAG, Memory, arbitrary writes or external side effects.

## Running

From `apps/api`, with Go configured as described in [AGENTS.md](../../AGENTS.md):

```bash
go run ./cmd/eval modes --live \
  --database-url "$TEST_DATABASE_URL" \
  --base-url https://your-model-endpoint.example/v1 --model YOUR_MODEL \
  --credential-env OPENAI_API_KEY \
  --trials 3 --approval-policy approve \
  --max-model-requests 60 --max-total-tokens 180000 \
  --sample-max-model-calls 16 --sample-max-tokens 20000 \
  --timeout 60s --suite-timeout 5m --enforce > mode-report.json
```

`--live` authorizes model calls; use a Tool/streaming/JSON-capable target and an
explicitly approved budget. The database URL must name a dedicated test database
and have `CREATEDB` plus normal AgentFlow schema/pgvector setup privileges.
The evaluator creates and drops its own database;
it never migrates the supplied database or uses application `.env` configuration.
Temporary materials are also removed. Keep the JSON report before exiting;
its synthetic inputs, configuration, Git revision, Run IDs and Replays are the
retained evidence. Credential values and the database URL are not report fields.

Multi always uses its real plan-approval endpoint. `approve` submits the generated
plan unchanged; `edit` appends the dataset's fixed instruction; `reject` cancels
the Run. Original/submitted plan hashes and the action are retained. Approval
does not grant file-write authority. Mode order rotates with each trial.

## Budgets and Results

Suite request/token/time limits cover **all** arms and trials. Request admission
adds a physical-attempt ceiling without replacing production owner/key/global
admission. Each arm reserves its full common per-Run token ceiling; only settled
unused capacity becomes available to the next arm. Insufficient capacity skips
the next arm rather than silently giving it a smaller ceiling. Unknown usage
stops further spending. Deadlines explicitly cancel accepted work and allow a
separate bounded cleanup window. Token reservations are estimates, not a guarantee
against provider-reported billing overshoot.

The report separates:

- `verified`, missing/incorrect facts, source evidence and constraint violations
  from the Runtime's completion status. `reviewed_but_unverified` means a completed
  answer failed the independent sensor despite a completed Review stage; it is
  not a semantic interpretation of the Reviewer's prose.
- Ledger logical model/Tool calls, admitted physical model attempts, tokens and
  nullable cost. No price table means unknown cost, not free execution.
- Active execution, automated approval wait and wall time; stage durations,
  iterations, exact repeated Act outputs (`no_progress_iterations`) and stop
  reason. Stage durations are persisted stage lifetimes, not pure GPU time.

Failures, cancellations, recoverable states and `not_evaluated` arms stay in the
denominator. P95 is intentionally `null` for this small profile. Deltas are paired
against Single in the same trial and disabled for incomplete, unsettled or
drifted arms. `--enforce` checks this **evidence/comparability gate**, not that
every answer is correct: a completed wrong answer is a valid negative result.
Exit codes are `0` for a report, `1` for an enforced gate failure, and `2` for
configuration/infrastructure errors. No conclusion that more stages improve
quality follows from a longer trace or deterministic fixture responses.

## Regression Evidence

```bash
TEST_DATABASE_URL="$TEST_DATABASE_URL" \
EVALUATION_REPORT_DIR=/private/tmp/mode-evidence \
go test ./cmd/eval ./internal/evaluation/modeeval \
  -run 'TestModeAblation|TestCommonBoundary|TestIncompleteEvidence' -count=1
```

Only provider transport is stubbed. The focused gate covers three rotated trials,
fact omission, malformed output, missing read receipts, Reviewer false acceptance,
repeated iterations, approval edit/rejection, provider outage/timeout, cancellation,
per-Run and shared budgets. Pure report-contract checks retain recoverable
outcomes and suppress misleading deltas. These are functional fixture results,
not live quality measurements or a new process-kill/Resume drill; use the existing
[recovery drill](../operations/release-recovery-drill.md) for crash recovery.
