# Evidence Comparison View

The dedicated **Evaluation > Run comparison** page compares two Runs from the
same Workspace. The view is read-only and reuses the existing Replay, Episode
Report, Model Request metadata, and Run-list endpoints. It does not persist a
new report or create an online evaluation service.

## Use

1. Open a Run's Replay page.
2. Open **Evaluation** and select **Compare with another run**.
3. Choose the baseline Run; the current Run is preselected.
4. Select **Compare evidence**.

Run comparison is intentionally separated from normal Replay because its
deltas are meaningful only for repeated or controlled single-variable tests.
Replay remains the operational view for inspecting one Run.

The comparison shows both Run IDs, task and material identity, execution mode,
Agent, model, frozen snapshot hash, outcome, Verification status, stop reason,
tokens, model and Tool calls, duration, errors, final output, Context Manifest
references, Model Request references, citations, and persisted Tool or
Verification Artifact references. Missing values are shown as `Unknown`.

## Comparability

The browser compares the two persisted frozen snapshots. Snapshot timestamps
are ignored. Task, retrieved-material identity, snapshot schema, execution mode,
and delegation identity must match. Runtime differences are grouped into the
following predeclared experiment variables:

- Agent definition;
- Model;
- Tool set;
- Context assembly;
- Run budget;
- Tool governance;
- orchestration limits.

A pair is comparable when all runtime inputs match or exactly one of those
variables differs. Multiple variable changes, unknown required identity,
unrecognized runtime changes, or task/material/mode/delegation drift make the
pair review-only. In that state the UI lists every failed comparability check
and does not calculate deltas. This prevents an unrelated Run from being
presented as an improvement.

The current runtime does not persist a separate experiment ID, so that field is
explicitly `Unknown`; the two Run IDs and snapshot hashes remain visible. A
single pair is diagnostic evidence and does not replace the multi-trial reports
produced by the offline evaluation CLI.

## Reproducible Local Pair

For the shortest controlled demonstration:

1. Create a Single Agent with Memory and RAG disabled.
2. Start two new Conversations and send the exact same prompt with that Agent.
3. Do not change the model, Tools, Context settings, Run Budget, or orchestration
   limits between Runs.
4. Open one completed Run's Replay page, then select **Evaluation > Compare with another run** and choose the other Run.

The pair should pass as an identical-runtime comparison. To demonstrate a
single-variable comparison, change exactly one declared dimension, create a
third Run with the same prompt, and compare it with the original baseline.
Ordinary Runs with different prompts or retrieved materials are expected to be
review-only.

## Access Boundary

No comparison endpoint bypasses existing access control. Candidate Runs and all
evidence are loaded through Workspace-scoped APIs with capture content disabled.
An inaccessible baseline therefore behaves like any other scoped Run read and
is not returned to the browser.
