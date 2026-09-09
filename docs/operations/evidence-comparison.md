# Evidence Comparison View

The dedicated **Evaluation > Run comparison** page compares two Runs from the
same Workspace. The view is read-only and reuses the existing Replay, Episode
Report, Model Request metadata, and Run-list endpoints. It does not persist a
new report or create an online evaluation service.

## Use

1. Open **Run comparison** from the Workspace sidebar.
2. Choose the current and baseline Runs.
3. Select **Compare evidence**.

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

## Access Boundary

No comparison endpoint bypasses existing access control. Candidate Runs and all
evidence are loaded through Workspace-scoped APIs with capture content disabled.
An inaccessible baseline therefore behaves like any other scoped Run read and
is not returned to the browser.
