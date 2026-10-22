# File and Skill Task Evaluation

`eval tool --profile file_skill` compares independent Single-Agent Runs with
and without one bound Skill. Both arms keep the same task, Agent prompt, model,
sampling, filesystem authority and Run budgets. The declared experiment variable
includes the Skill metadata, loading schemas and progressively loaded method.
The default `artifact` profile remains unchanged.

## Outcome and Gate

The versioned [synthetic dataset](../../examples/tools/file-skill-v1/dataset.json)
contains a sourced report and an unrelated acknowledgment. Each trial rotates
arm order. The report must preserve current facts over a superseded value and
retain an exact quotation. Acceptance requires **source read -> committed file
write -> complete read-back**, matching the actual saved file and SHA-256.
The evaluator checks the outcome independently of the model's success claim.

Skill selection is scored separately: the relevant candidate must load the
bound package once and read a matching complete resource; request-backed
Manifest evidence must show its instructions were included. The unrelated task
must neither activate a Skill nor use Tools. Inclusion proves input, not that a
real model followed the method.

With `--enforce`, rollout fails for an unverified candidate, incomplete pair,
contract drift, unsettled usage/effects, unsafe Tool failure or unexpected file.
Correctable argument rejection remains visible but may pass after a valid retry.
Safety/uncertainty stops later samples; ordinary wrong answers do not erase
remaining trials. Failed and unrun samples remain in the denominator. Token
ratio and extra physical-request thresholds also gate the candidate; they are
explicit experiment settings, not calibrated production defaults. No command
automatically changes production enablement.

## Execution

Only the model HTTP transport is stubbed in the deterministic gate. It uses
production `app.New`, Chat HTTP commands, the Turn/Tool loop, real filesystem and
Skill Bindings, effect journal, Usage Ledger and Postgres Replay. Mode ablation
shares the disposable environment, not these task sensors.

From `apps/api`, supply a **dedicated test database URL with CREATEDB privileges**;
the runner creates and drops its own random database. It never opens application
data or loads `.env`. Temporary source/output roots are private; only the settled
evaluator-owned `report.md` is reset between arms. JSON retains file contents,
hash, Run IDs and Replay before cleanup. Keep reports private when using private
inputs. Cleanup failure returns an infrastructure error, not success.

```bash
EVALUATION_REPORT_DIR=/tmp/tool-evidence \
  go test ./cmd/eval -run '^TestFileSkill' -count=1
```

These integration cases require `TEST_DATABASE_URL`; without it they explicitly
skip. CI supplies Postgres and retains the reports. Fixture success is protocol
and sensor evidence, **not** live-model reliability or measured Skill benefit.

For a separately authorized Tool-capable live target, export its credential and
`TEST_DATABASE_URL`, then run:

```bash
go run ./cmd/eval tool --profile file_skill --live \
  --base-url https://api.example.com/v1 --model YOUR_MODEL \
  --credential-env OPENAI_API_KEY --database-url "$TEST_DATABASE_URL" \
  --dataset ../../examples/tools/file-skill-v1/dataset.json \
  --skill-dir ../../examples/tools/file-skill-v1/skills \
  --trials 3 --max-model-calls 60 --max-total-tokens 180000 \
  --sample-max-model-calls 12 --sample-max-tokens 30000 \
  --timeout 60s --suite-timeout 10m \
  --max-token-ratio 2 --max-extra-model-requests 3 \
  --enforce > /tmp/file-skill-live.json
```

In this profile, `--max-model-calls` caps **physical requests across the suite**;
`--sample-max-model-calls` is the production logical-call limit per Run. Retries
are disabled. Suite token admission reserves one full per-Run ceiling and
returns unused capacity only after settlement. These estimates cannot guarantee
provider billing; unknown pricing is `cost_micros: null`, never zero cost.

`file-skill-eval-v1` retains dataset/Git/package identities, frozen Run contracts,
configuration, outcome/selection findings, calls, usage source, latency and
paired deltas. Drift or incomplete/unsettled arms disable deltas. Exit codes are
`0` for a produced report, `1` for a failed enforced gate and `2` for invalid setup
or infrastructure/report errors; partial evidence is retained when available.

This first profile does not evaluate Search/Knowledge, citations from the web,
explicit Skill invocation, arbitrary Skill packages or external judges. A small
synthetic dataset is not representative quality calibration; representative
live trials and manual rollout review remain separate work.
