# Workspace Agent Configuration and Tool Availability

## Boundary

Agents belong to a Workspace, not directly to a login name. Workspace ownership
authorizes their configuration. Built-in templates are shared and read-only;
creating an Agent from a template makes an independent Workspace-owned copy.
Service Tool bindings, credentials and execution policy remain operator-owned.

The Chat Agent picker contains only Workspace-owned profiles. Templates appear
in the optional **New agent → Copy from** picker, not as selectable runtime
Agents. Copying changes only the draft until **Create Agent** succeeds. It does
not enable Workspace Tools or bypass service policy. This is a UI workflow change:
the API still exposes read-only templates, and orchestration's existing fallback
for modes without an explicit Agent is unchanged.

Effective Tools are the intersection of service availability, the Workspace
allowlist, the frozen Agent Tool selection, the current Agent selection, and the
current execution policy. Removing a selected Tool or archiving its Agent blocks
subsequent calls, even when the frozen protocol previously allowed it.
Snapshot definitions explain the original protocol; they never override current
revocation. Disabling a Tool affects subsequent calls, not a handler already in flight.
The Tools response separates `service_enabled`, `workspace_enabled`, effective
`enabled`, `excluded_reason`, and the persisted `config_revision`. Static policy
denials are included; argument-dependent scope and private-context egress decisions
are evaluated at call time and recorded in Run Tool policy events.

On first use, a Workspace saves the currently service-enabled Tool names as its
initial allowlist. An explicitly empty list allows nothing. Later installed Tools
are denied until explicitly enabled. Prerequisite failures and operator-disabled
Tools cannot be enabled by a Workspace owner.
An archived Workspace can read its existing configuration but cannot change or
execute it; reading an uninitialized archived Workspace creates no grants.
Operator recovery callbacks (Retry/Compensate) also honor current service and
Workspace availability. Manual effect confirmations do not execute a Tool and
remain available to settle an uncertain prior result.

## Regression Inventory

| Boundary | Success | Failure |
| --- | --- | --- |
| Agent storage | Owner create/read/update/archive round-trip | Other Workspace cannot list/read/mutate; template mutation rejected |
| Tool configuration | Independent toggles survive reload | Empty allowlist, missing Tool and service disable fail closed; new Tool does not inherit permission |
| Runtime | Single, Multi-Agent and Autonomous use the same scoped configuration | Foreign/archived Agent rejected before Chat writes; revoked Tool and Resume cannot bypass current grants |
| Execution | Authorized call reaches Binding | Revoked call emits typed denial without entering Binding |
| Browser | Owner can edit Agent and Workspace Tool settings | Workspace switch discards previous drafts and requests; archived Workspace is read-only |
| Migration | Explicit source/target mapping preserves contents and historical references | No owner guessing; no automatic startup assignment; transaction and backup before apply |

## Legacy Data

Unassigned, non-template Agents are not public templates and are not exposed by
Workspace APIs. Migration requires an operator-selected target Workspace. Existing
IDs and historical Run references are retained for the primary target; additional
owners receive new independent Agent IDs. Never rewrite historical Snapshots.

Stop the old API and take a restricted-access PostgreSQL backup first. Preview
the assignment, then apply the same explicit targets:

```sh
go -C apps/api run ./cmd/migrate-workspace-agents --workspace <primary-id> --copy-workspace <optional-copy-id>
go -C apps/api run ./cmd/migrate-workspace-agents --workspace <primary-id> --copy-workspace <optional-copy-id> --apply
```

The command initializes the current schema and read-only templates but changes
legacy ownership only with `--apply`. The assignment is transactional, refuses
inactive/missing targets or active Runs, and is a no-op when repeated. Unassigned
Agents are retained but hidden until assigned. Historical Runs in other Workspaces
remain readable; they cannot resume using a now-foreign Agent. Reverting to an
older globally scoped API would reopen access and is not a safe authorization rollback.

## Verification

The backend suite uses disposable PostgreSQL databases when `TEST_DATABASE_URL`
is configured. Focused browser gates exercise production Next.js/Go composition
with a signed OIDC fixture and isolated Postgres:

```sh
AGENTFLOW_IDENTITY_TEST=1 bash scripts/test-browser.sh workspace-agent-config.spec.ts resource-authorization.spec.ts
bash scripts/test-browser.sh runtime.spec.ts
```

The first gate attaches `workspace-agent-config-evidence.json`, recording both
authenticated owners, their four Workspace IDs, the owned Agent and the checks.
The runtime gate covers Single, Multi-Agent and Autonomous Tool continuations,
durable streaming and failure propagation. These are deterministic provider/IdP
fixtures, not live-model quality measurements or a production security audit.
