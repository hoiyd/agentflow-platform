# Sandbox Command Execution

PROD-101 uses the local Docker `sbx` CLI as the execution boundary, not bare
host `exec`. The first release supplies an opt-in `sandbox_command` Tool through
the existing Catalog, Executor, frozen definitions, Effect Journal and Artifact
governance. It does not execute Skill resources or change Command Verifier's
trusted-local host execution behavior.

## Failure Inventory

| Failure | Required outcome |
| --- | --- |
| Disabled, missing CLI, unavailable daemon or Docker login | No guest command and no host fallback |
| Unallowlisted executable, excessive arguments, relative executable | Reject before creating a sandbox |
| Concurrent requests exceed the sandbox cap | Bounded capacity error; no unbounded waiting queue |
| Startup or policy setup fails | Do not run the requested command; remove any partial sandbox |
| Command reads/writes host paths or host credentials | Mountless VM; no host workspace, SSH agent or inherited API environment |
| Command or descendants access the network | Per-sandbox deny-all rule, independent of global allow rules |
| Nonzero exit or resource limit | Return exit status and bounded output, not success |
| Cancellation or timeout | Independent cleanup context destroys the sandbox and descendants |
| Excessive/binary output | Bound retained text, sanitize it, retain observed count/hash |
| Cleanup cannot be confirmed | Retain ownership record and deny further work until recovery |
| API process crashes | Next startup reclaims recorded owned sandboxes before admitting new work |
| API process crashes after a command may have run | Effect Journal blocks automatic replay of an uncertain call |
| Configuration changes before Resume | Definition revision changes; existing frozen Tool checks reject drift |

## Boundary

Each call receives a new mountless shell sandbox. The writable directory exists
only inside the VM; it is not the browser user's machine, the API working tree,
or an AgentFlow Workspace filesystem. No input/output file sync, host mounts,
port publishing, MCP configuration, remote sbx, or Skill-script activation is exposed
to model arguments. Only structured argv for operator-allowlisted guest
executables is accepted. A shell is usable only if the operator grants it.

Docker owns microVM isolation and network enforcement. AgentFlow owns lifecycle,
bounded concurrency/time/output, minimal process environment and durable call
receipts. See [Docker's local security model](https://docs.docker.com/ai/sandboxes/security/)
and [mountless shell creation](https://docs.docker.com/reference/cli/sbx/create/shell/).

The guest runs as UID/GID `65534`, with `no_new_privs`, an empty environment
except PATH/locale/guest HOME, 64 processes, 128 descriptors, no core dumps,
16 MiB per-file size and an address-space cap of half VM RAM. These are inherited
process limits, not a total disk quota or fair-use scheduler. Guest execution uses
the trusted shell template's `env`, `setpriv`, `prlimit` and `timeout` utilities;
missing utilities fail instead of reverting to a less restricted launch.

This is a single-API-process integration, not a multi-worker sandbox scheduler.
Do not mount the API repository or credential files to make a command work.
The existing host Command Verifier remains local-only and is not upgraded to a
sandbox by this feature. Skill installation/loading still grants no execution.

## Enable and Authorize

Use an authenticated local Docker sbx installation that supports mountless
`create shell`, `--deny-network '**'`, `--skills off`, numeric `exec --user`, and
`rm --force`. Check `sbx create shell --help` before enabling. Older CLIs lacking
the shared-Skills opt-out are intentionally unsupported; unknown flags must
fail, never be dropped. No global network/secret policy is changed by AgentFlow.

For an existing Homebrew installation on macOS, update the installed cask:

```bash
brew update
brew upgrade --cask docker/tap/sbx
sbx version
sbx create shell --help
```

The `--skills=off|readonly|readwrite` interface was introduced in sbx 0.43.0;
prefer the current stable release rather than relying on the deprecated
`--no-share-skills` alias. See [Docker release notes](https://docs.docker.com/ai/sandboxes/release-notes/).
If a previously started daemon is still running the old version, restart it with
`sbx daemon restart` after checking for active sandbox work; this can interrupt
other sandboxes and must not be done automatically by an AgentFlow Run.

```bash
sbx login
sbx create shell --help
sbx ls --json
```

Before the first sandbox, the operator must initialize the machine-wide network
policy. Docker recommends Balanced for normal development:

```bash
sbx policy init balanced
```

Do this only when no global policy has been initialized; do not reset an existing
policy automatically. This affects other sbx sandboxes, but AgentFlow still applies
its own explicit `--deny-network '**'` rule to every VM. AgentFlow never initializes
or relaxes global policy itself. See [Docker's local access controls](https://docs.docker.com/ai/sandboxes/governance/access-controls/local/).

`sbx login` opens Docker OAuth in a browser. This authenticates the operator's
local sandbox service; it is separate from an AgentFlow model API key and from
logging in to a container registry with `docker login`. Do not put Docker login
tokens into the API `.env` or guest environment. See
[Docker installation and sign-in](https://docs.docker.com/ai/sandboxes/install/).
An empty sandbox inventory is fine; an authentication/daemon error is not. A
successful listing only proves inventory access, not global policy initialization
or VM execution readiness: run the live gate below to verify
actual scratch execution, isolation and cleanup before enabling the Tool.

Set `SANDBOX_ENABLED=true` in the API configuration and restart it. Defaults and
bounded settings live in [`.env.example`](../../apps/api/.env.example). Do not
change the operator's environment automatically from a Run. Each call has no
host workspace mount, uses `--skills off`, and omits SSH-agent forwarding.
Pin `SANDBOX_TEMPLATE` to a trusted immutable image digest for reproducibility;
the default official shell tag is mutable, so its exact image bytes are **not**
frozen by AgentFlow.

In the existing tools configuration, add `sandbox_command` to `enabled_tools`
and add the following entry to `security_policy.rules`, preserving other rules:

```json
{
  "id": "operator-sandbox-command",
  "tool": "sandbox_command",
  "action": "allow_and_log",
  "capability": {
    "source": "local",
    "scope": {
      "resources": [{"kind": "filesystem", "name": "sbx:scratch", "access": "write"}],
      "network": {"mode": "none"}
    },
    "side_effect": "internal_write",
    "visibility": "user",
    "audit_level": "full"
  }
}
```

The rule is an operator grant, not an approval supplied by a model or Skill.
Bind the Tool in an Agent's existing Configure dialog. Single, Multi-Agent and
Autonomous use the same Binding. Only the trusted local operator currently
edits global Tool/Agent configuration; OIDC consumers can use a preconfigured
Agent without acquiring configuration privileges.

Example structured arguments:

```json
{"args": ["/usr/bin/python3", "-c", "print(sum(range(10)))"]}
```

`exit_code != 0` is unsuccessful guest execution, even when the CLI transport
returned normally. The Tool result is an observation, not a Completion Gate;
enabling this Tool does not automatically verify an Agent's answer. Host Command
Verifier and its frontend restrictions stay unchanged.

## Evidence and Recovery

The result contains sandbox identity, policy revision, exit code, bounded combined
stdout/stderr, observed raw byte count/hash, truncation and cleanup confirmation.
Large JSON results reuse existing Tool Artifacts; bytes beyond the runner's
retained output cap are not archived. Binary/NUL text is sanitized for persistence;
hashes describe observed raw bytes, not the sanitized preview. Output is untrusted
data and follows existing trace/Artifact redaction and authorization.

Frozen Tool definitions include the runner profile revision. Changing command
allowlists, resources, timeout, output cap or template reference rejects old
Resume through existing revision validation. The request hash retains the exact
structured argv. This does not preserve a guest filesystem or automatically
resolve changed image tags.

The Effect Journal uses real Turn/Stage identity. Committed duplicate calls replay
their receipt without creating another VM. Unknown attempts require existing
reconciliation rather than automatic retry. Durable sandbox ownership files are
fsynced before creation; a single-owner directory lock prevents two API processes
from managing the same state. Startup deletes only exactly recorded sandboxes
before admitting work. Failed deletion must be confirmed absent by `sbx ls --quiet`
or remain blocked; no global prune is used.

Normal completion and cancellation remove the entire VM using an independent
30-second cleanup context. Graceful shutdown cancels/drains the runner before
waiting for accepted Runs, keeping the Store open for receipt settlement; the
server allows 35 seconds for shutdown when sandboxes are enabled. If the API
is killed, reclamation occurs at the next startup, not instantly; the guest's own
wall timeout still bounds its command. Local sbx does not supply a per-call total
disk quota or automatic VM deletion on API death. Preserve the state directory;
if cleanup fails, repair Docker login/daemon and restart, rather than deleting
ownership files. This first release is not suitable as a publicly exposed,
hostile multi-tenant code service without additional deployment controls.

## Repeatable Validation

```bash
cd apps/api
go test ./internal/sandbox ./internal/tool ./internal/httpapi -race -count=1
# Requires a real authenticated, compatible sbx installation; never fakes a VM.
AGENTFLOW_SANDBOX_TEST=1 SANDBOX_TEST_EVIDENCE_PATH=/tmp/sandbox-execution-evidence.json \
  go test ./internal/sandbox -run '^TestLiveSBXIsolationAndCleanup$' -v -count=1 -timeout=15m
```

The live gate checks scratch writes, inaccessible/unchanged host canaries, absent
host credentials/SSH forwarding, `no_new_privs`, inherited process limits and
actual address-space/file-size/descriptor rejection, denied public TLS/metadata requests,
nonzero exit, large output and cancellation only after guest output arrives.
The public request must also have an explicit local-rule denial in sbx's policy
audit, with no allowed hosts for that VM. TCP handshake success alone is not an
egress test: sbx's transparent proxy may accept TCP before rejecting upstream
access. The audit is retained in `sandbox-execution-evidence.json`. An unsuccessful live gate means
real isolation has **not** been verified, regardless of unit-test coverage.

The separate browser gate uses a **controlled CLI fixture**, production app
composition and disposable Postgres. It validates the Agent UI binding, actual
model Tool continuation, persistent receipt, frozen definition, and Replay reload:

```bash
cd apps/web
TEST_DATABASE_URL='<dedicated-test-postgres-url>' AGENTFLOW_SANDBOX_BROWSER_TEST=1 \
  npm run test:e2e -- sandbox-command.spec.ts
```

It attaches `sandbox-command-browser-evidence.json`, but proves protocol and
persistence, **not** a microVM's isolation. Backend HTTP integration covers all
three modes; the CLI process fault harness covers creation/deletion failure,
timeout, output bounds, environment and restart ownership.
