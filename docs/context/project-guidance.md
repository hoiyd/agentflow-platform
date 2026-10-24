# Scoped Project Guidance

Project guidance supplies reviewed conventions for one explicitly selected project.
Reading a file does not make it trusted guidance. Guidance cannot grant Tools,
credentials, filesystem access or command execution.

## Configure And Use

1. Configure read-only project roots using [scoped filesystem access](../tools/scoped-filesystem.md).
2. Set `PROJECT_GUIDANCE_PATH` to an operator-owned JSON file outside model-writable
   output directories. Empty disables new project selections. Trust is separate
   from `FILESYSTEM_ROOTS_PATH`, Workspace Tool grants and Agent bindings.
3. List the exact entry and rule files to trust:

```json
{
  "projects": [{
    "workspace_id": "1",
    "root": "project-alpha",
    "entry": "GUIDE.md",
    "rules": [{"path": "report-rules.md", "paths": ["reports/*.md"]}]
  }]
}
```

Use the actual owned Workspace ID and logical root. The repository has two
synthetic projects under [`examples/project-guidance`](../../examples/project-guidance).
Their entry files and report conventions are intentionally different.

In the workbench, open **Project** beside the message input, choose a project,
select up to 16 task files and **Apply project**. The selection is visible for
new Runs in all three modes and retained for that Workspace in the browser tab.
Cancel leaves it unchanged; **Clear project** removes it. Existing Runs keep
their frozen guidance. The UI cannot create trust records or grant file Tools.

HTTP and the [headless CLI](../runtime/headless-run-client.md) accept the same selection:

```json
{
  "agent_id": "<owned-agent-id>",
  "mode": "single",
  "message": "Read reports/input.md and write a report using this project's conventions.",
  "project_context": {"root": "project-alpha", "paths": ["reports/input.md"]}
}
```

The selector accepts 1–16 concrete existing regular files, relative to one root.
Rules use Go `path.Match`: `*` does not cross `/`; recursive `**`, imports,
absolute paths and traversal are unsupported. At most 16 rules with 16 patterns
each follow one required project entry. Unmatched rule files are not read.
There is no automatic `AGENTS.md`/`CLAUDE.md` discovery.
To read task materials, the Agent still needs separately authorized file Tools.

`GET /api/project-guidance/projects` returns currently authorized logical roots;
`GET /api/project-guidance/projects/{root}/files?path=.` lists at most 500 entries
in one directory. Both use the request's Workspace and prohibit caching. Discovery exposes no host
paths or file contents; linked, sensitive or unsupported selection names are
omitted. Task paths containing `*?[]` are not supported. The composer checks
discovery support before sending, so older backends cannot ignore the selection.

## Context And Recovery

New Runs freeze the selected entry/rules, exact UTF-8 contents, SHA-256 hashes,
matched relative paths and root/config identities in `runtime_snapshot.project_guidance`.
The text is JSON-wrapped below platform policy and the current user request.
It is required Context; it is not compressed into conversation history.

Identical guidance bodies are loaded once. Instructions identical to an active
Skill are not repeated, and matching Memory text is suppressed. The Manifest
records `project_guidance`, `content_hash`, `source_path`, matches, selection
reason and actual framed token cost; unmatched and duplicate sources remain
explainable. Replay's **Saved project guidance** shows frozen text, hashes, matches
and body-token estimates. Inspect `context.assembled` for framed token costs;
`agentflow result` / `GET /api/runs/{id}/replay` return the typed frozen content.

Resume and Tool continuations use frozen text, never hot-load modified guidance.
Before each model assembly and on Resume, current Workspace/root access and the
previously selected trust records are checked. Removing them blocks continuation
before the next provider request; it does not recall an already in-flight request.
Changing text affects new Runs. New follow-up Runs inherit the prior project
selection and freeze current text, independently of the current composer draft;
ordinary Chat requests must select explicitly.

## Limits And Failures

Each guidance file is limited to 16 KiB; total observed text to 32 KiB; unique
guidance bodies to 4,096 estimated tokens. JSON framing and policy also count
toward the ordinary model input capacity. Whole instructions are rejected rather
than silently truncated. A loading failure returns a typed error before creating
a Run, so it has no Context Manifest; total input overflow after assembly uses
the existing Manifest and input-budget error.

| Failure | Outcome |
| --- | --- |
| Missing/foreign/untrusted root, revoked trust or root drift | Reject; do not fall back to global guidance |
| Bad configuration/pattern, duplicate source path or unsupported import field | Reject configuration |
| Symlink, hardlink, non-regular file, sensitive path, invalid text or missing matched file | Reject through the scoped reader |
| Credential-shaped content | Reject, not redact into a different trusted instruction |
| File/total/token budget exceeded | `project_guidance_budget_exceeded`; no provider request |
| Text changes during a paused Run | Resume keeps frozen text; a new Run captures the update |
| Discovery of foreign roots, sensitive paths or linked files | No host paths/content disclosed; scoped reader checks still apply |
| Project/file reads complete after selection changes or the dialog closes | Discard obsolete results; never apply another project's files |
| Cancel, clear or Workspace change | Cancel preserves the saved choice; clear removes it; another Workspace never inherits it |
| New request reaches an older backend without guidance support | Reject before Chat; never silently omit the selected guidance |

Bounded guidance text is persisted even when Request Capture is metadata-only;
operator trust configuration explicitly opts these files into Run evidence.
Snapshots contain logical roots and relative paths, not absolute host paths.
Project context is conservatively private for network Tool policy. Trust/hash
records do not prove instructions harmless or eliminate prompt injection.
Snapshots without this optional field retain their previous behavior; no new
database column or Snapshot-version migration is required.

## Verification

With `TEST_DATABASE_URL` pointing to a disposable Postgres base, run from `apps/api`:

```bash
EVALUATION_REPORT_DIR=/tmp/project-guidance go test ./app -run '^TestScopedProjectGuidanceProductionLifecycle$' -count=1
```

The retained `project-guidance-evidence.json` contains CLI/Replay evidence for two
projects and a no-guidance control, actual model inputs, restart/frozen continuation
and fresh follow-up selection. It also checks Memory deduplication, revoked trust,
corrupt snapshots and permission claims that cannot grant Tools or network access.
Real file Bindings and Postgres execute; a controlled provider echoes received
conventions. This proves plumbing and boundaries, not live adherence or quality.

From the repository root, verify provider delivery and Replay reload in a browser:

```bash
AGENTFLOW_PROJECT_GUIDANCE_TEST=1 TEST_DATABASE_URL=postgres://.../agentflow_test \
  bash scripts/test-browser.sh project-guidance
```

Playwright retains API/workbench/navigation/Workspace evidence JSON and desktop
screenshots. It checks selection, all modes, cancel/clear/reload, stale reads,
query failures, compatibility rejection and readable Replay provenance. Both
gates use synthetic project text, not private conversations or live quality data.
