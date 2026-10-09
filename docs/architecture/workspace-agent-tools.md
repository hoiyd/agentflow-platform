# Workspace Agent Configuration and Tool Availability

## Ownership

Agents belong to a Workspace, not directly to a login name. Workspace ownership
authorizes their configuration. Built-in templates are shared and read-only;
creating an Agent from a template makes an independent Workspace-owned copy.
Service Tool bindings, credentials and execution policy remain operator-owned.

The runtime picker lists only created Workspace Agents; see
[creation and selection](../runtime/agent-profiles.md#creation-and-selection).
Templates remain available through the API and as Multi-Agent routing fallback
when no owned profiles exist. Copying a template does not grant Tool access.

## Tool Authorization

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

Omit `--copy-workspace` if no additional copy is needed. The command initializes
the schema and templates but assigns ownership only with `--apply`. Assignment is
transactional and idempotent, and refuses inactive/missing targets or active Runs.
Historical Runs in other Workspaces
remain readable; they cannot resume using a now-foreign Agent. Reverting to an
older globally scoped API would reopen access and is not a safe authorization rollback.

## Verification

Backend persistence tests use disposable PostgreSQL when `TEST_DATABASE_URL` is
configured. Browser commands, retained evidence and fixture limitations live in
[Workspace configuration gates](../operations/functional-regression-testing.md#workspace-configuration).
