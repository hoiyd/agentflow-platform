# Live Compaction Fidelity and Continuation

EVAL-003 compares full history with a production-generated compaction, then
continues the same synthetic task. It is an offline experiment, not a new
online compaction policy. Model quality requires an explicitly authorized live
report; deterministic HTTP fixtures prove protocol and failure handling only.

## Failure Inventory

| Failure | Expected evidence |
| --- | --- |
| Lost/changed amount, currency, relationship, negation or correction | Typed fact probe fails; candidate gate blocks |
| Canceled work revived or uncertainty presented as certain | Probe/outcome constraint fails, regardless of token savings |
| Fabricated source, quote or unobserved material read | Outcome rejected against frozen source and Tool receipts |
| Summary clipped to its configured cap | Retain actual stored summary and test it, not the unbounded response |
| Orphan Tool protocol boundary | Reject dataset before any request |
| Provider error, timeout, cancellation or exhausted shared budget | Retain failed/unrun samples in the denominator; never retry blindly |
| Full history does not fit | Mark pair infeasible, not an improvement over a truncated baseline |
| Compactor failure | Keep original history unchanged; record fallback, not successful compaction |
| Unsettled usage | Stop later spending; retain ledger and unknown cost |

## Run

From the repository root, with `OPENAI_API_KEY` already exported:

```bash
mkdir -p .cache/compaction-evidence
umask 077
go -C apps/api run ./cmd/eval context-live --live \
  --model MODEL --base-url https://api.openai.com/v1 \
  --trials 3 --max-model-calls 24 --max-total-tokens 180000 \
  --timeout 60s --context-window-tokens 32768 --summary-max-tokens 1024 \
  --enforce > .cache/compaction-evidence/report.json
```

Use a Tool-capable OpenAI-compatible model and the target's actual context
window. Run from a clean revision: Git identity records `HEAD`, not uncommitted
edits. The command does not load `.env`. Without `--live`, a credential, a
model and positive budgets it refuses to run. Exit codes: `0` report generated,
`1` gate failed with `--enforce`, `2` invalid configuration or report I/O failure.
Without `--enforce`, inspect `gate.passed`; exit zero is not a quality claim.

The **entire suite** shares calls/tokens across summary generation, diagnostic
probes, Tool follow-ups and both arms/all trials. Retries are disabled. Each arm
has the same configured deadline and four-Tool ceiling; remaining suite budget
can stop either arm. An open usage reservation stops subsequent phases and
samples. Provider usage is authoritative when available; estimates and open
reservations remain visible. Monetary cost is `null`/unknown, not zero.
`physical_requests` includes transport compatibility fallbacks, which also use
the shared request ceiling; ledger `model_calls` counts logical operations.
Token reservations are estimate-based: a provider-reported overrun is retained
and stops spending rather than being clipped to pretend the limit held.

## Experiment and Sensors

[`compaction-continuation.v1.json`](../../examples/context/compaction-continuation.v1.json)
freezes one synthetic invoice task: correct amount/currency/payee, forbid release,
cancel a notice, preserve an unknown receipt and exact identifiers. Expected
claims and their source quotations are manually authored. Neutral historical
padding creates pressure without adding facts. Only complete user/assistant
exchanges are admitted; arbitrary Tool histories are rejected, not flattened.

Each arm gets its own Conversation, Run, material Artifact and legal Task State
Revision. State is built from the same explicit user-source patch; content/hash
and version match, but revision/message IDs are never copied between Conversations.
The report maps logical sources to each arm's actual identities. Model, sampling,
input, materials, Tools and assembly limits are shared. Arm order rotates by trial.
The full-history preflight rejects any dropped message, before either arm spends
quota. An infeasible pair has no improvement delta.

The candidate **forces the production Compactor's overflow path** at this legal
boundary; it does not claim that a real provider overflow occurred or measure
the online soft/hard trigger policy. The stored, possibly clipped summary is the
one actually tested. No production prompts or compression algorithm are replaced.

Two checks answer different questions:

- **Fidelity probe:** a separate model extraction receives raw history or the
  generated summary, without Task State, material or the answer key. Deterministic
  sensors compare typed values and exact source ID/quotation against authored
  truth, including negation, relationships, cancellations and uncertainty. This
  is a task-specific extraction probe, not an LLM Judge or proof of arbitrary
  semantic completeness. Exact quotation requirements can reject paraphrases.
  This first dataset puts the probed facts in the compacted older sources.
- **Continuation:** the production Turn Engine/Tool loop receives the arm's
  history/summary and required Task State. A real scoped `artifact_read` Binding
  must supply the immutable supporting material before the final structured
  report. Sensors check the decision facts, exact material citation and actual
  successful read receipt, not just citation-like text in the answer.

Any critical candidate violation blocks `gate.passed`. A successful raw-history
fallback after summary failure is retained but is **not** successful compaction.
Failures, cancellation, budget skips and infeasible pairs stay in the denominator.
Quality checks do not automatically modify production policy or retry a bad
summary; continuation has only read Tools in this isolated experiment.

## Report and Regression Evidence

`live-compaction-continuation-v1` reuses the Evaluation Report identity envelope.
It retains Dataset/Git/model/sampling/configuration identity, canonical input and
boundary hashes, source mappings, state revisions, summary generation/hash,
manifests, event identities/payload hashes, extracted claims, continuation output,
Tool evidence and the complete Usage Ledger. Synthetic source text is retained;
credentials, endpoint URLs and unrestricted production captures are not.
Fixture Run IDs are evidence identities, not links to production Replay.

`phases` separates compaction, fidelity probe and continuation usage/wall time.
`pairs` separates summary cost, continuation token/time deltas and **total** token
delta including diagnostic probes. Positive deltas mean the candidate spent more.
Undefined/incomparable deltas must not be interpreted as zero savings; consult
`incomparable_reason`; incomparable deltas are JSON `null`. There is no
small-sample p95 or aggregate intelligence score.

Run the focused network-free regression gate (loopback HTTP only):

```bash
cd apps/api
EVALUATION_REPORT_DIR=/tmp/compaction-fixtures \
  go test ./internal/evaluation/contexteval ./cmd/eval \
  -run 'TestLiveCompactionContinuation|TestLiveContextCLI' -count=1
```

Reports explicitly label `deterministic_http_fixture`; CI's existing
`offline-evaluation-reports` artifact retains them for 14 days. Fixture cases
record their scenario, expected gate and per-scenario reproduce command alongside
the report envelope. They cover the failure inventory; the timeout fixture is a provider `408`, not a
measurement of real provider latency. Ordinary CI makes no public model calls.
No UI/API/database migration is needed; this profile uses an isolated in-memory
Store and does not test deployment recovery or all orchestration modes.

## Recorded Live Evidence

The [2026-10-17 DeepSeek Flash report](evidence/live-compaction-deepseek-flash-2026-10-17.json)
is the first retained live result, generated from clean revision
`52966aec31b4c8fc1efdbb633c828a3c38ccedb9` using the unchanged v1 Dataset and sensors.
**The experiment completed, but its quality gate failed. No compaction benefit
or compression ratio was established.** All three candidates fell back to raw
history, making every pair incomparable; the null deltas are not zero savings.
Candidate probes and continuation therefore used raw history, not a generated
summary; their findings are not measurements of summary fidelity.

| Evidence | Report field / observation |
| --- | --- |
| Trials / arms | `config.trials=3`; `summary.samples=6` |
| Shared authorized limits | 24 physical requests; 180,000 tokens; 60 seconds per arm; operator outer deadline 10 minutes |
| Actual usage | `physical_requests=21`; `usage.total_tokens=115543`; no open reservations |
| Wall time | `completed_at - started_at`: approximately 110.047 seconds |
| Material access / source integrity | Six complete `material_reads`; all six `history_unchanged=true` |
| Compaction | All three compaction phases report `incomplete_output`; all candidate `raw_history_fallback=true` |
| Quality / comparability | `summary.verified=0`; `summary.comparable_pairs=0`; `gate.passed=false` |
| Summary generation expense | Sum of compaction `phases[].usage`: 3 calls, 18,319 tokens, 33,337 ms |
| Monetary cost | `cost_micros=null`; all 21 entries have unknown price, not free usage |

The compaction phases each settled exactly 2,048 completion tokens, matching
`context_assembly.output_reserve_tokens`. Their recorded reasoning-token counts
were 1,437, 1,927, and 2,048. This is evidence of output-headroom pressure on this
reasoning-capable target, not proof that a generated summary lost facts: no
successful summary was produced. Full-history trial 1 also had an incomplete
fidelity probe. No provider retry or automatic second experiment was performed.

All continuation phases completed and read the material, but failed the strict
outcome sensor. Findings include exact-quote boundaries (for example, including
`Correction:` where the authored quote omits it), extra historical citations
where the sensor requires exactly one material citation, and receipt values
`pending`/`unconfirmed` instead of the required `unknown`. These are reported
contract failures, not automatically fabricated evidence or compaction-caused
drift. Even the completed full-history arms did not pass both checks.

Before a separately authorized rerun, review the shared generation headroom and
align quotation/citation instructions with the declared sensor contract. Keep
this report and Dataset identity intact; do not rescore it, weaken the gate, or
attribute fallback outcomes to compressed history. Fallback pair-level cost
defaults are not measured zero cost: use the sample phases and ledger above.

To reproduce the profile, explicitly authorize a new spend, use the recorded
revision, export the provider credential without committing it, and run:

```bash
OPENAI_API_KEY="$DEEPSEEK_API_KEY" go -C apps/api run ./cmd/eval context-live --live \
  --model deepseek-flash --base-url https://api.deepseek.com \
  --trials 3 --max-model-calls 24 --max-total-tokens 180000 \
  --timeout 60s --context-window-tokens 128000 --summary-max-tokens 1024 \
  --enforce > /tmp/compaction-live-report.json
```

The recorded command exited `1` because `--enforce` rejected the gate, not
because report generation failed. Sampling is retained in `sampling`; the model
is a mutable provider alias, so a rerun need not reproduce identical outputs.
Only synthetic source/task data is published; credentials, endpoints and local
paths are absent from the JSON report. It does not contain production Run data.

This small task cannot establish lossless arbitrarily long sessions, independent
Task State benefit, or justify background/parallel compaction or advanced
anti-drift mechanisms. Production compaction policy is unchanged.
