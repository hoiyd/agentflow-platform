# Headless Run Client

The CLI operates the same authenticated HTTP API and durable Runs as the
workbench. It does not embed a Runtime, evaluator or independent permission model.

## Run a Task

Start the API normally. From the repository root (or install the binary with
`go -C apps/api build -o /tmp/agentflow ./cmd/agentflow`):

```bash
export AGENTFLOW_API_URL=http://localhost:8080
export AGENTFLOW_WORKSPACE_ID='<owned-workspace-id>'
# Edit agent_id and root/path names for your existing authorized Agent/project.
bash scripts/agentflow.sh start --request examples/headless/file-report.json \
  --output /tmp/report-evidence.json > /tmp/report-events.ndjson
```

The example requires an owned Agent with `file_read`/`file_write` and operator
configured `project`/`output` roots. It cannot read arbitrary client-machine
files: Tools execute on the API's configured roots under normal authorization.
The final JSON object is accepted by the existing local Schema verifier, not
assumed correct because generation finished. Adapt the
[structured-output example](../../examples/structured-output/report-request.json)
for tasks that do not need filesystem Tools.

`--request` is an existing OpenAPI `ChatRequest`, bounded to 2 MiB. It accepts all
three modes, Agent/Conversation identity, read-only planning, Knowledge filters
and Completion Contracts without maintaining a parallel request format. Optional
[`project_context`](../context/project-instructions.md) selects path-scoped project instructions;
it does not grant file access or Tools.

```bash
bash scripts/agentflow.sh start --request task.json --detach
bash scripts/agentflow.sh observe --run <run-id> --after <last-durable-sequence>
bash scripts/agentflow.sh result --run <run-id> --output /tmp/new-evidence.json
bash scripts/agentflow.sh cancel --run <run-id>
```

`start` waits by default; `--detach` returns the acknowledged Run/Conversation ID
and canonical request SHA-256. Neither Ctrl-C nor the total `--deadline` (default
`5m`) cancels execution. `cancel` sends one explicit API command; its success
acknowledges the command and can still show `canceling`. Observe for the terminal
state. There is no automatic approval, Continue, Resume, answer repair or Run retry.
Use the workbench for human decisions/recovery, then observe the same Run again.

## Existing Identity

Trusted-local mode needs no credential. It remains unauthenticated and must not
be exposed as a multi-user deployment.

OIDC mode reuses an existing **AgentFlow session**, not an IdP password/access
token. Save the current session cookie from your browser's developer tools in
an owner-only file outside the repository: one line
`agentflow_session=<value>`, or `__Host-agentflow_session=<value>` on HTTPS.
Do not put the value in shell commands/history. Then configure:

```bash
chmod 600 "$HOME/.config/agentflow/session"
export AGENTFLOW_SESSION_FILE="$HOME/.config/agentflow/session"
export AGENTFLOW_WEB_URL=http://localhost:3000 # exact server AUTH_WEB_URL origin
```

Flags `--api-url`, `--workspace`, `--web-url` and `--session-file` override these
environment variables. `scripts/agentflow.sh` also loads these four settings from
ignored `scripts/.env`; exported values take precedence. Installed CLI binaries
read process environment only. See [configuration files](../operations/backend-configuration.md#configuration-files).
Only the file path may appear in argv. Expired/revoked
sessions fail; this slice does not add device login, automatic refresh, service
accounts or token issuance. Secure cookies require HTTPS. API/Web settings are
origins, not URLs with paths, queries or credentials; API redirects and ambient
proxies are disabled, and HTTP is accepted only for loopback.

## Output and Recovery

- `start`/`observe` write NDJSON: `submitted` (start only), `event`, `snapshot`,
  then `result`. `result` writes one JSON report; `cancel` writes the API Run.
  Errors are safe JSON on stderr with Run identity/cursor when known.
- Durable events advance the cursor only after the complete frame is emitted.
  Up to five reconnects use `Last-Event-ID`; replayed sequences are deduplicated.
  Keep the NDJSON and pass the last consumed positive `event.sequence` to a new
  process. Live-only events have no durable cursor and cannot be reconstructed
  token-for-token; persisted output/checkpoints and Replay remain authoritative.
- If the creation response is lost before acknowledgement, `submission_unknown`
  retains the known Conversation ID and request hash. Inspect Recent runs or
  the authorized Runs API before deciding to submit again. No create deduplication
  protocol is claimed, and the client never retries the POST automatically.
- A result report contains Run/Workspace/Conversation identity, frozen Runtime
  Snapshot, ordered persisted events, Verification, H-51 `structured_result`,
  Usage Ledger with sources, Artifact/effect references, API Replay URL and
  workbench Replay path (full link when `--web-url` is supplied). It is the same
  Run the web page reads. It does not automatically download referenced Artifacts.
- `request_sha256` is present when this process submitted the request; a later
  result-only process cannot infer that request. Preserve the input JSON too.
  Client build metadata records revision/dirty state when available, otherwise
  `unknown`; the frozen Snapshot records the server's effective configuration.
- `--output` creates a new `0600` evidence file without overwriting an existing
  path. Reports/NDJSON can contain authorized prompts, responses and file data:
  keep them private. Safe diagnostics do not make exported content public-safe.
  Replay JSON is bounded to 32 MiB and individual SSE frames to 2 MiB; exceeding
  either is an explicit failure, not a truncated success.

| Exit | Meaning |
| --- | --- |
| 0 | Completed accepted result, detached submission acknowledged, or Cancel command accepted |
| 1 | HTTP/transport/protocol/output failure (including foreign resources hidden as 404) |
| 2 | Invalid command/config/input |
| 3 | Failed/recoverable Run, failed Verification or unavailable contracted machine value |
| 4 | HTTP 401/403 authentication/authorization rejection |
| 5 | Waiting for user/approval, or result queried before completion |
| 6 | Canceled Run observed/exported |
| 7 | Submission outcome unknown; inspect before retrying |
| 8 | HTTP 429 capacity rejection; safe Retry-After is included when provided |
| 124 / 130 | Client deadline / interruption, after identity is known (or before submission starts) |

An interruption during unacknowledged submission uses exit 7 because work may
already exist. Waiting/failure/cancellation still exports readable evidence;
provisional text is never promoted into a verified object. Restarted services can
serve persisted evidence; the CLI does not provide automatic active-Run takeover.

## Failure Contract

| Condition | Required behavior |
| --- | --- |
| Rejected/expired session or foreign Workspace | Fail without bypassing authorization or retrying submission |
| Admission rejection | Report HTTP status and Retry-After; never resubmit automatically |
| Submission response lost before Run ID | Report `submission_unknown`; never create another Run automatically |
| Observation disconnect, truncated frame, lag or API restart | Reconnect from the last completely emitted durable sequence; deduplicate replayed events |
| Invalid sequence, oversized frame or foreign Run identity | Reject protocol data; never advance the cursor |
| Waiting for user/approval | Export the current evidence and exit; never approve or wait indefinitely |
| Verification failure | Preserve evidence; do not expose a provisional object as verified output |
| Deadline or Ctrl-C | Stop the client only; use the separate cancel command to stop execution |
| Broken stdout or output-file failure | Return a failure; never silently claim retained evidence |
| Redirect, unsafe API URL or exposed credential file | Reject; do not forward the session to another destination |

## Verification

From `apps/api`, with a dedicated disposable Postgres:

```bash
TEST_DATABASE_URL='<dedicated-test-postgres-url>' \
EVALUATION_REPORT_DIR=/tmp/headless-evidence \
  go test ./app -run '^TestHeadlessClientProductionRunLifecycle$' -count=1
go test ./internal/runclient -count=1
```

The first gate builds the CLI and exercises production composition, signed OIDC
configuration/session authorization, real filesystem/Skill/Calculator Bindings,
Postgres, Single/Loop strict output, Multi approval pause and failed Verification. It also checks
detached execution, capacity rejection, SIGINT versus explicit running Cancel,
approval pause and a new application instance reading the same durable Run.
`headless-client-evidence.json` retains fixed synthetic inputs, command, identities,
configuration and results; backend CI already uploads this report directory.
Wire-fault tests cover truncated/disconnected/lagged streams, deduplication,
invalid identity/sequence/JSON, credential boundaries and consumer failure.
These are CLI/API functional tests, not browser UX or live-model quality evidence.
