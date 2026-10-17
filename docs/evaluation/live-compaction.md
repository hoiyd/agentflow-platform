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
| Generation exhausts its output budget | Retain `incomplete_output` and fallback; a larger stored-summary cap is not a generation-budget fix |
| Output reserve leaves no input space | Reject before contacting the provider; never silently normalize an explicit experiment limit |
| Unsettled usage | Stop later spending; retain ledger and unknown cost |

## Run

From the repository root, with `OPENAI_API_KEY` already exported:

```bash
mkdir -p .cache/compaction-evidence
umask 077
go -C apps/api run ./cmd/eval context-live --live \
  --model MODEL --base-url https://api.openai.com/v1 \
  --trials 3 --max-model-calls 24 --max-total-tokens 180000 \
  --timeout 60s --context-window-tokens 32768 \
  --output-reserve-tokens 8192 --summary-max-tokens 1024 \
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

`--output-reserve-tokens` controls per-request generation headroom, including
reasoning where counted by the provider. The default remains 2,048; the 8,192
example is an explicit calibration candidate, not a measured optimum. It applies
equally to summary generation, probes and both continuation arms and participates
in usage reservations. `--summary-max-tokens` independently caps the **stored**
summary (default 1,024); increasing generation headroom does not enlarge it.
An explicit reserve plus the 512-token safety margin must leave positive input
space within the context window. Both requested limits and effective assembly
configuration are retained in the report. No production defaults are changed.

## Experiment and Sensors

[`compaction-continuation.v3.json`](../../examples/context/compaction-continuation.v3.json)
freezes one synthetic invoice task: correct amount/currency/cap account, forbid release,
cancel a notice, preserve an unknown receipt and exact identifiers. Expected
claims and their source quotations are manually authored. Neutral historical
padding creates pressure without adding facts. Only complete user/assistant
exchanges are admitted; arbitrary Tool histories are rejected, not flattened.

Dataset v2 corrected three quotation boundaries to include `Correction:`.
V3 changes the ambiguous `payee` field to `cap_account`: the source identifies
the account to which the cap applies, not necessarily a payment recipient.
History, materials, Task State and truth values are otherwise unchanged.
The extraction prompt distinguishes user decisions from assistant
acknowledgments and uses retained original quotes rather than summary paraphrases.
It requires complete source sentences, preserves explicit unknowns and declares
exactly one material citation, separate from fact provenance. Exact-value,
source/quote checks and real read receipts remain strict.
The [v1](../../examples/context/compaction-continuation.v1.json) and
[v2](../../examples/context/compaction-continuation.v2.json) Datasets are retained;
old reports must not be rescored. Prompt hashes, Dataset/sensor versions and the
compaction algorithm distinguish profiles; comparing revisions does not isolate
an algorithm-only effect.

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
  `fact_values_passed` and `fact_provenance_passed` distinguish lost values from
  wrong attribution/quotes; both remain necessary for `passed`. A summary-only
  probe has no original-history read Tool: lost quotes cannot be reconstructed
  by treating a paraphrase as an original. V3's retained Source Evidence is
  included in the tested summary, not injected from the answer key.
  Sensor v3 accepts either the logical Dataset source ID or its recorded
  physical Message ID in the same arm. This is identity normalization only:
  a different user/assistant source, foreign ID or altered quote still fails.
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
The source-boundary integration also covers foreign/protected Messages, wrong
authors, invented quotes, malformed/over-budget evidence and preservation of
the previous generation; these deterministic checks do not prove that a real
model follows the authority or no-speculation prompt rules.

## Calibrated Live Sample

The [2026-10-17 calibrated report](evidence/live-compaction-deepseek-flash-2026-10-17-calibrated.json)
uses clean revision `ba6313df346485a919ecfe915c12c08fca9a3505`, Dataset v2 and
`deepseek-flash`. **A complete summary was generated, stored and used for
continuation, without truncation or raw-history fallback. The quality gate
still failed; successful summary generation is not verified fidelity.**
This report predates the v3 evidence protocol and split sensors; it is not a
live validation of those changes.

| Evidence | Report field / observation |
| --- | --- |
| Authorization | One trial / two arms; at most 8 requests, 90,000 tokens, 120 seconds per arm and a five-minute outer deadline; no automatic rerun |
| Generation / stored cap | `config.output_reserve_tokens=8192`; `config.summary_max_tokens=1024` |
| Actual use | `physical_requests=7`; `usage.total_tokens=32572`; no open reservations; approximately 40.148 seconds in the report |
| Summary | Candidate `compaction.status=completed`, generation 1; full text in `samples[].compaction.summary` |
| Estimated context reduction | `before_tokens=5214`, `after_tokens=1007`, `reduction_ratio=0.8068661296509397`; assembler estimates including the protected recent history, not provider-measured token savings |
| Summary generation expense | Candidate compaction phase: 5,389 tokens, 6,474 ms |
| Evidence integrity | Both histories unchanged; two complete material reads, each matching its frozen Artifact hash |
| Quality | One execution-comparable pair; `summary.verified=0/2`; `gate.passed=false` |
| Monetary cost | `cost_micros=null`; all seven model settlements have unknown prices |

The full-history continuation passed. Its fidelity probe preserved all typed
values but cited the assistant acknowledgment `h6` for the canceled notice,
instead of the authored user-source `h5`. The compacted continuation also
preserved all eight typed values and passed material-read/citation checks, but
six fact quotations differed from the frozen originals. The summary-only probe
additionally returned a null payee, although Account-B appears in the stored
summary. This distinguishes extraction failure from a demonstrably absent fact;
neither is silently accepted by the gate. Manual inspection also finds added
questions about invoice currency and report scope, not explicit source claims.

Pair-level `outcome_regressed=true` records the baseline continuation pass and
candidate provenance failure. The aggregate `regressed=0` instead compares full
verification, which neither arm achieved; it is **not** evidence of no regression.
The measured total-token delta is -98, including the summary and probes; one
failed-quality pair cannot establish useful savings or justify a policy change.
Multiple settings changed, so this sample does not isolate a causal budget effect.

Reproduce only after authorizing another spend, using the recorded revision:

```bash
OPENAI_API_KEY="$DEEPSEEK_API_KEY" go -C apps/api run ./cmd/eval context-live --live \
  --model deepseek-flash --base-url https://api.deepseek.com \
  --trials 1 --max-model-calls 8 --max-total-tokens 90000 --timeout 120s \
  --context-window-tokens 128000 --output-reserve-tokens 8192 \
  --summary-max-tokens 1024 --enforce > /tmp/compaction-calibrated-report.json
```

The operator enforced the five-minute outer deadline separately. Exit `1` is the
expected `--enforce` gate result, not a transport or report-generation failure.
That calibration experiment did not change production policy. Its provenance
failures motivated the v3 source-evidence protocol; the following experiment
tests that protocol without changing or rescoring the calibration report.

## Source Evidence Live Result

The [v3 live report](evidence/live-compaction-deepseek-flash-2026-10-17-source-evidence-v3.json)
uses clean revision `5abb2cff2fbe3265af307bcd44e0fc823b391736`, Dataset v3,
sensor v3 and `deepseek-flash`. **Original-quote fidelity improved, but overall
quality did not pass: the candidate confused a canceled task's status with its
allowed next action.** The full-history arm passed both checks; the compacted
arm failed both. `summary.verified=1/2`, `summary.regressed=1`, `gate.passed=false`.

| Candidate check | Prior calibrated sample | V3 sample |
| --- | --- | --- |
| Complete stored summary without fallback | Yes | Yes |
| Probe: correct typed values | 7/8 | 7/8 |
| Probe: exact original source/quote | 0/8 | 7/8 |
| Continuation: correct typed values | 8/8 | 7/8 |
| Continuation: exact original source/quote | 2/8 | 8/8 |
| Candidate quality gate | Failed | Failed |

Counts derive from each report's expected fact set and `fact_value:` /
`fact_provenance:` findings, not a new scoring of old output. Historical profiles
share the same history, materials, Task State, model alias and budgets, but
prompts, sensors and the account field name changed. This is descriptive
single-sample evidence, not a statistical or single-variable quality claim.

Actual use was 7 physical requests and 37,865 tokens, with zero open reservations
and approximately 59.554 seconds of report wall time. The authorization remained
8 requests / 90,000 tokens, 120 seconds per arm and a five-minute outer deadline;
no automatic rerun occurred. Monetary cost is unknown, not zero. Both histories
were unchanged and both material reads completed with matching Artifact hashes.

The summary retained four validated evidence records. Its estimated surface
fell from 5,214 to 1,103 tokens (78.85% reduction including the protected tail).
The candidate spent 3,927 more total tokens than its current full-history arm,
including compaction and probes; its continuation alone used 4,538 fewer tokens.
Neither number establishes useful savings while the quality gate is failing.

The remaining failure is specific: `notice` became `draft_only` in the probe
and `send_canceled_draft_only` in continuation instead of `canceled`. The summary
introduced a draft-notice pending item despite retaining the original
cancellation quote. Persisted Task State still has `send-notice.status=canceled`;
there was no external action or observed mutation of that state. Verbatim
evidence therefore does not guarantee correct interpretation of task scope.
The next quality work should separate task lifecycle from permitted actions
and prevent unsupported pending work, not remove the cancellation check or
silently rerun until it passes.

To reproduce, authorize a new spend and use the command above at this recorded
revision; its default Dataset is v3. Exit `1` again reflects `--enforce`, not an
execution or transport failure. Originals and the prior report remain intact.

This small task cannot establish lossless arbitrarily long sessions, independent Task State benefit,
or justify background/parallel compaction or advanced anti-drift mechanisms.
