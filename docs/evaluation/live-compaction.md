# Live Compaction Fidelity and Continuation

This experiment compares full history with a production-generated summary, then
continues the same synthetic task. It measures a narrow quality/cost question,
not a new online compaction policy. Fixtures prove the protocol; explicitly
authorized live reports measure the selected model/profile.

## Run

From the repository root with the repository Go version and `OPENAI_API_KEY`
already configured:

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

Use a Tool-capable target and its actual context window, from a clean revision.
The command does not load `.env`; live flag, credential, model and positive budgets
are required. Exit `0` means a report was generated; with `--enforce`, gate failure
returns `1`. Configuration/report I/O errors return `2`. Always inspect `gate.passed`.

The suite shares calls/tokens across all arms/trials, summary, probes and Tool
follow-ups; retries are disabled. Each arm has the same deadline/four-Tool ceiling.
Transport compatibility fallbacks consume physical-request allowance. Unsettled
usage stops spending; estimates/provider overruns remain visible. Unknown monetary
cost is `null`, not zero.

`output-reserve-tokens` is per-request generation headroom (default 2,048);
`summary-max-tokens` caps stored summary (default 1,024). They are different limits.
The 8,192 example is not a measured optimum. Reserve plus 512-token safety margin
must leave positive input space; the report keeps requested/effective values.

## Experiment and Sensors

The frozen [v4 Dataset](../../examples/context/compaction-continuation.v4.json)
contains invoice facts, corrections, a canceled notice, an explicit unknown and
exact source quotations. Neutral padding creates history pressure without adding
facts. Only complete user/assistant exchanges are accepted, not flattened Tool histories.

Each arm has its own Conversation/Run/material Artifact/Task State Revision;
logical facts match but physical IDs are never copied between arms. Model,
sampling, input, materials, Tools and limits are shared; arm order rotates. Full
history must fit without dropping Messages or the pair is infeasible.

The candidate forces the production Compactor's overflow path at a legal boundary.
It tests the actually stored summary, including clipping, not the unbounded model
response. It does not measure online soft/hard trigger quality or a real provider overflow.

| Sensor | Inputs and acceptance |
| --- | --- |
| Fidelity probe | Raw history or summary only; no answer key, Task State, material or recovery Tool. Typed values and exact original source/quotes must match authored truth |
| Continuation | Production Turn Engine/Tool loop plus required Task State. Must read immutable material through `artifact_read`, then satisfy typed facts, citations and actual successful read receipts |

Source Evidence in a summary is tested content, not injected answer-key material.
Logical source IDs and same-arm physical IDs normalize to the same source; foreign
IDs/roles/altered quotes still fail. `fact_values_passed` and
`fact_provenance_passed` separate values from attribution, both required for pass.
Exact quotation checks can reject paraphrases; this is not a general semantic Judge.

V4 separates task status from allowed next action and checks definitions against
authored Task State before spending. Both arms see field meanings and full enum,
not current status answers. The production v4 summary also rejects generated
operational sections. Neither change coerces model answers or proves truth.

[v1](../../examples/context/compaction-continuation.v1.json),
[v2](../../examples/context/compaction-continuation.v2.json) and
[v3](../../examples/context/compaction-continuation.v3.json) remain retained.
**Old reports must not be rescored.** Dataset/Prompt/sensor/algorithm versions
distinguish profiles; changing several cannot isolate an algorithm-only effect.

## Failure Inventory

| Failure | Expected outcome |
| --- | --- |
| Lost value, relationship, negation/correction; revived canceled work | Typed fact/outcome fails regardless of token savings |
| Invented source/quote or unobserved read | Frozen-source/receipt check fails |
| Summary clipped | Test actual stored summary, never the unbounded candidate |
| Broken protocol or nonpositive input capacity | Reject before model access |
| Full history does not fit | Infeasible pair; no improvement delta |
| Compactor failure/incomplete generation | Preserve originals; fallback is not successful compaction |
| Timeout, cancellation, exhausted budget | Keep failed/unrun samples in denominator |
| Unsettled usage | Stop spending; retain uncertainty |

Critical candidate violations block the gate. Raw-history fallback is not compaction
success; continuation has read-only Tools and never automatically retries a bad summary.

## Report and Regression Evidence

`live-compaction-continuation-v1` retains Dataset/Git/model/sampling identity,
input/boundary hashes, arm source mappings, state, actual summary/hash, Manifests,
Events, claims/output, Tool receipts and Usage Ledger. Synthetic text is retained;
credentials/endpoints and unrestricted production Capture are not. Fixture IDs
are not production Replay links.

`phases` separates summary/probe/continuation cost. `pairs` distinguishes
continuation deltas from total deltas including probes/summary; positive means
candidate spent more. Incomparable deltas are `null`, never zero savings.

Focused deterministic gate, from `apps/api`:

```bash
EVALUATION_REPORT_DIR=/tmp/compaction-fixtures \
  go test ./internal/evaluation/contexteval ./cmd/eval \
  -run 'TestLiveCompactionContinuation|TestLiveContextCLI' -count=1
```

Reports label `deterministic_http_fixture` and include scenario/expected gate/
reproduce command; CI retains its offline artifact for 14 days. A fixture 408
tests failure behavior, not real latency. Integration covers source boundaries,
atomic fallback, operational-section rejection and canceled-status/action confusion.
It does not prove live prompt compliance, deployment recovery or all modes.

## Retained Live Results

All three reports below used one trial with at most 8 requests/90,000 tokens,
120 seconds per arm and an operator-enforced five-minute outer deadline. Each used
7 physical requests, settled usage, unchanged histories and hash-matching material
reads; monetary cost is unknown. Counts are original report results, not rescoring.

| Profile / raw report | Gate and interpretation |
| --- | --- |
| [Calibrated v2](evidence/live-compaction-deepseek-flash-2026-10-17-calibrated.json) | Failed: summary generation/continuation succeeded, but exact-source fidelity did not; `summary.verified=0/2` |
| [Source Evidence v3](evidence/live-compaction-deepseek-flash-2026-10-17-source-evidence-v3.json) | Failed: full-history arm passed; candidate conflated canceled status and draft action; `summary.verified=1/2`, one regression |
| [Task lifecycle v4](evidence/live-compaction-deepseek-flash-2026-10-17-task-lifecycle-v4.json) | Passed one comparable pair: both arms preserved eight typed facts/quotes; `summary.verified=2/2`, no fallback |

### Calibrated Live Sample

The v2 report at `ba6313df346485a919ecfe915c12c08fca9a3505` used 32,572 tokens.
The raw-history probe misattributed a user cancellation to an assistant acknowledgment;
the candidate also lost exact quotes. Pair outcome regression and aggregate
verification regression have different denominators: `regressed=0` was not proof
of no harm. Reduced surface size did not establish useful savings while quality failed.

### Source Evidence Live Result

The v3 report at `5abb2cff2fbe3265af307bcd44e0fc823b391736` used 37,865 tokens.
Original quotations improved, but `canceled` became `draft_only` or a combined
status/action value. Persisted Task State remained canceled; there was no observed
external mutation. Verbatim evidence alone did not ensure correct interpretation.

### Task Lifecycle Live Result

V4 used 33,496 tokens. Its pair continuation delta was -6,159 tokens; total
including summary/probes was -1,456. Estimated surface fell from 5,214 to 524 tokens;
that is not provider-measured input savings. Both checks passed, with canceled
status and draft-only next action kept separate.

The raw report captured base HEAD while changes were uncommitted. The
[source identity record](evidence/live-compaction-deepseek-flash-2026-10-17-task-lifecycle-v4.source.json)
maps the unchanged report to `tested_source_commit` with source hashes; reproduce
that commit, not raw HEAD alone. One successful synthetic pair cannot establish
general reliability, independent Task State benefit or lossless long sessions.
Field definitions and summary policy changed together, so v3/v4 is not a
single-variable causal comparison. A new run requires fresh spend authorization.
