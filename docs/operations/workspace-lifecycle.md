# Workspace Lifecycle

Privacy retention remains active for archived/deleted spaces: expired Capture
and Tool Artifact payloads may only be cleared, never rewritten or resurrected.

## Failure Inventory

PROD-017 changes both resource identity and authorization. Verify these failures
before accepting the implementation:

| Failure | Required outcome |
| --- | --- |
| Legacy namespace has no owner or multiple owners | Preview reports the conflict; apply requires an explicit owner mapping and makes no partial data changes |
| Migration is interrupted or repeated | Transaction rollback or idempotent completion; existing resource IDs and content remain intact |
| Another user submits a Workspace ID | No read, mutation, default selection or ownership change |
| Name is empty/too long or status invalid | Reject without modifying the entity |
| Default/last active Workspace is archived or deleted | Require an owned active replacement; never leave an invalid default |
| Workspace contains an unfinished Run or uncertain effect | Reject closing the Workspace; do not silently cancel execution |
| Execution races with archive/delete | A shared database lock serializes admission and the lifecycle mutation |
| An old tab writes to archived/deleted Workspace | Read-only rejection or not found, never global-default fallback |
| User logs in again after deletion/revocation | Do not recreate the removed Workspace or grant |
| Rename, refresh or switch Workspace | Stable ID, persistent readable name, no stale requests/cross-space cache |

The first release excludes sharing, invitations, ownership transfer and physical
purging. OIDC still owns credentials; local mode remains trusted development.

## Entity and Defaults

`workspaces` persists `id`, `owner_user_id`, `name`, `description`, `status`,
`created_at`, `updated_at`, and `deleted_at`. PostgreSQL generates the BIGINT ID;
names contain 1-80 characters and descriptions at most 2000. Names are not unique
and never act as identifiers. API IDs remain **decimal strings**, preserving
existing client/domain identifier shapes and avoiding JavaScript BIGINT rounding.
`id` is the only stored entity identifier. Related tables use BIGINT
`workspace_id` foreign keys referencing `workspaces.id`; there is no mirrored
`workspace_id` column on the entity table. Conversion to a decimal string occurs
only in the Go/API representation.

Each entity has one immutable owner; one User can own many spaces. Membership is
an owner-constrained revocation gate, not a second ownership model. The existing
`auth_personal_workspaces` table retains onboarding history and the user's current
default reference. Its historical name does not mean the default is immutable.
Revoking access or deleting a space does not delete that marker, so repeat OIDC
login cannot implicitly restore access or recreate an old space.

New verified OIDC users receive one owned Personal workspace transactionally.
Local mode uses reserved `user_local` with a separate owned Personal workspace:
it is a database owner, **not** a login credential or browser-supplied identity.
The old `default_workspace` also needs an explicit owner; it has no exemptions.
Clients cannot supply an owner or generated ID through create/update requests.

## Lifecycle and UI

The top toolbar displays entity names. **Workspace settings** opens a compact
dialog for creating/editing spaces, choosing a default, archiving/restoring, and
confirming deletion. Switching reloads scoped consumers. A saved browser selection
is checked against the current authoritative list before consumers mount.

| State | Behavior |
| --- | --- |
| Active | Normal owned resource access and execution |
| Archived | Owned reads/search allowed; business writes, Chat, evaluation, Resume and mutation controls disabled; restore explicitly |
| `deleted_at` set | Hidden from normal list; stale/foreign requests return 404; resource rows remain retained |

Deletion is soft and cannot be undone through this UI. Physical purge/retention
administration remains separate. Archiving/deleting the default requires another
owned active granted space as replacement in the **same transaction**. Closing the
last active space returns 409. Create an alternative first. A nonmember can
explicitly create a new space from Workspace settings; this is not an implicit
regrant of any existing revoked space.

Unfinished Runs (`queued`, `running`, `waiting_for_user`, `failed_recoverable`,
`canceling`) or unresolved effects (`executing`, `needs_reconciliation`,
`reconciling`) block closing. Finish/cancel and reconcile them first; the API does
not silently cancel tasks. Owner row locks serialize default/lifecycle changes;
business-write triggers hold shared entity locks, conflicting with closure's
exclusive row lock. Execution is not kept inside a long-running DB transaction.
Late writes after archive/delete fail even if an earlier HTTP admission succeeded.
Exact erasure of expired Capture/Artifact payloads is the only write exception;
retention cannot rewrite metadata or resurrect content.

## API Boundary

`GET/POST /api/workspaces`, `GET/PATCH/DELETE /api/workspaces/{id}` operate on the
current owner, independently of any stale selected-space header. OIDC session
and mutation Origin checks still apply. PATCH supports `name`, `description`,
`status`, `make_default`, and `replacement_workspace_id`; DELETE accepts the
replacement as a query parameter. Unknown body fields and multiple JSON values
are rejected. See [OpenAPI](../../api/openapi.yaml) for DTOs.

Business requests use the owned selected space or the owner's active default
when omitted. Explicit header/query/body selectors must agree. Archived writes
return 409; foreign, revoked, deleted and nonexistent spaces return 404 without
disclosing another owner's entity. Old string IDs are **not** runtime aliases.
`personal_workspace` in the session response now means the current authorized
active default; its existing field name is retained for compatibility.

Shared Agent profiles, Tool switches/security configuration, provider credentials
and trusted Skill packages remain service-wide. OIDC owners may read/use available
profiles and Tools but cannot mutate shared Agent/Tool configuration. Trusted-local
operators manage it; private per-Workspace Agent configurations are not added here.
PROD-002 still owns the comprehensive object-authorization audit. Owner entity
checks do not prove a complete public multi-tenant security boundary.

## Legacy Migration

For old databases, run from the repository root:

```bash
bash scripts/workspace-migrate.sh
bash scripts/workspace-migrate.sh --owner '<legacy-space>=<existing-user-id>'
# Stop API traffic and take a FULL database backup before apply.
bash scripts/workspace-migrate.sh --owner '<legacy-space>=<existing-user-id>' --apply
```

The wrapper loads `apps/api/.env` and passes all flags to the Go command.
`--owner` is repeatable, `--timeout` defaults to `2m`, and no `--apply` means a
genuinely read-only preview (no startup DDL, seeds or assignments). A namespace
with exactly one existing owner is inferred. Missing/multiple ownership requires
an explicit existing User mapping; `user_local` can be selected deliberately.
Unknown users, conflicting rerun mappings and ambiguity reject the whole apply.
An explicit reassignment establishes the chosen owner's grant; an existing
owner's revoked grant is not restored just by repeating its mapping.

Apply takes table/advisory locks and rewrites all affected namespaces in one
transaction, so numeric old namespaces cannot collide through sequential string
replacement. Conversations, Messages, Runs, collaboration rows, Memory records,
Documents, Task State and authorization references receive the new IDs. Resource
IDs/content remain intact. Task State's current root scope is updated; immutable
event payloads, captured prompts and runtime protocol snapshots stay historical
evidence, never arbitrarily searched/replaced. `workspace_migrations` records the
mapping for auditing/repeatability, not runtime alias lookup. Cross-owner old grants
are removed, revocation markers retained, and ownership/reference FKs installed.

Databases created by the earlier generated-alias layout are upgraded by the same
transaction: reference columns become BIGINT, FKs point directly to `id`, and the
entity's redundant `workspace_id` column is dropped without `CASCADE`. Numeric
IDs, names, defaults and resource contents stay unchanged. Startup backfills skip
the upgraded integer columns. Stop the old API and back up before applying this
layout change; the old binary still depends on the removed alias.

Executing/queued/canceling Runs block apply; finish/cancel them **before** stopping
the old API. All legacy spaces must have an owner before applying; there is no
partial migration that mixes numeric entities and legacy aliases. Deferring one
ambiguous space also defers live apply, but does not prevent testing in disposable
databases. The upgraded API refuses startup with outstanding old references.

After apply, restart the API and verify names, defaults, resource counts and owner
access. Repeated apply with the same mapping is idempotent. New databases bootstrap
the entity/constraints and local default automatically. A pre-lifecycle binary is
not a safe rollback on the upgraded DB: restore the matching full backup and
binary with traffic stopped. Do not drop new tables or guess a reverse mapping.

## Repeatable Evidence

Use a dedicated `TEST_DATABASE_URL` with CREATEDB privileges:

```bash
go -C apps/api test ./internal/store ./internal/identity ./internal/httpapi \
  -run 'TestWorkspace|TestPersonalWorkspace|TestOIDC|TestAuthenticatedWorkspace' -count=1
AGENTFLOW_IDENTITY_TEST=1 bash scripts/test-browser.sh workspace-lifecycle.spec.ts identity.spec.ts
```

The browser gate uses real Next.js, production Go composition, disposable
Postgres and signed OIDC identities, not mocked business endpoints. It retains
`workspace-lifecycle-evidence.json` and `workspace-owner-boundary-evidence.json`
under ignored Playwright results. Backend cases cover migration rollback/rerun,
numeric collision, persistent references, owner/default FKs, revoked-login behavior,
late writes and unfinished/uncertain execution. These are deterministic functional
boundaries, not live IdP configuration, performance or a complete PROD-002 audit.
