# Identity and Workspace Membership

OIDC identity, AgentFlow-owned login/registration presentation and personal
Workspace onboarding. Passwords stay at the identity provider; this is not a
complete account-management product. The companion [resource authorization](resource-authorization.md)
guide covers object ownership and live-stream access checks.

## Failure Inventory

| Failure | Required outcome |
| --- | --- |
| Missing, forged, revoked or expired session | 401 before any business handler executes. |
| Valid identity without owned/granted selected Workspace | 404 without disclosing another owner's space; never auto-enroll. |
| Header/query/body select different Workspaces | Reject; body cannot bypass authenticated scope. |
| Forged identity headers or an unverified email | Cannot establish identity or grant membership. |
| Invalid state, missing transaction cookie, nonce or PKCE mismatch | Reject login; no session created. |
| Invalid token signature, issuer, audience or expiry | Reject via the OIDC verifier; additional audiences or a foreign authorized party are not trusted. |
| Reused callback | Consumed login transaction cannot be used again. |
| Cross-origin mutation or logout | Reject before execution. |
| OIDC discovery, exchange or persistence unavailable | Fail closed, without exposing tokens or credentials. |
| Invalid authentication configuration | Refuse startup; never silently fall back to local mode. |
| Restart or membership removal | Sessions remain verifiable; removed memberships stop granting access. |
| Explicit sign out, followed by sign in | Old AgentFlow session is revoked; the next authorization request requires provider reauthentication. |
| Close and reopen a browser tab without signing out | Existing session remains valid until its absolute expiry; no logout on tab close. |
| Authentication disabled for trusted local use | Reserved persistent local User owns its own entities; no OIDC owner's data is implicitly accessible. |
| Duplicate or concurrent first login | One personal Workspace and one Membership; existing grants remain unchanged. |
| Personal Workspace grant fails | Marker and grant roll back together; no new Session; a fresh login retries. |
| Membership revoked after onboarding | Relogin/restart does not restore it; empty Membership is not a new-account signal. |
| Schema upgrade or API restart | Database Memberships remain unchanged; revoked grants are not restored. |

## Modes and Scope

`AUTH_MODE=local` is the default for trusted development. A persistent server-owned
`super` (display name **Super**) owns its Workspaces, but this establishes
**no browser-authenticated identity** or OIDC administrator role. `local` remains
the authentication mode; `super` is only its reserved server-side User ID.
Do not expose it to untrusted users. `AUTH_MODE=oidc` enables login, revocable
sessions and server-side membership checks before every business request,
including SSE, Replay, Artifact, Memory and knowledge endpoints. Only health,
the five authentication endpoints and CORS preflight are exempt. Every verified
OIDC login ensures that the user's personal Workspace has been provisioned once.

Registration uses the IdP's native account creation through `prompt=create`.
The endpoint is always available in OIDC mode, but the IdP alone controls whether
account creation is permitted. AgentFlow provisions an owned personal Workspace
entity and owner-constrained Membership. [Workspace lifecycle](workspace-lifecycle.md)
adds creation, rename, defaults, archive/restore and soft deletion. There is no local
password storage, password proxy, invitation, role matrix or service-account token.
Agent/Tool configuration remains service-wide and is read-only for ordinary OIDC
users; trusted-local operators maintain it. Workspace-private Agent configuration
is not implemented. [Resource authorization](resource-authorization.md) audits
the current object surface and its cross-owner failure paths. This boundary alone
does not make the deployment a public multi-tenant SaaS.

## Configure an OIDC Provider

Register a confidential OIDC authorization-code client with S256 PKCE. Set the
exact callback URL, then configure the API process:

```dotenv
AUTH_MODE=oidc
OIDC_ISSUER=https://identity.example.com/realms/agentflow
OIDC_CLIENT_ID=agentflow
OIDC_CLIENT_SECRET=<client-secret-in-process-environment>
OIDC_REDIRECT_URL=https://agentflow.example.com/api/auth/callback
AUTH_WEB_URL=https://agentflow.example.com
ALLOWED_ORIGINS=https://agentflow.example.com
AUTH_SESSION_TTL=8h
```

Issuer, discovery endpoints, callback and frontend require HTTPS, except on
loopback. Frontend and API must share the **same scheme and hostname**; local
development can use different ports. Deploy the API behind the same HTTPS origin
as Next.js or use an equivalent reverse-proxy arrangement. Set
`NEXT_PUBLIC_API_BASE_URL` to the browser-facing API origin (empty means same-origin)
when building the web application. Do not mix `localhost` and `127.0.0.1`.

In OIDC mode, a verified first login (including an existing IdP account)
grants **only that identity's own personal Workspace**, never a shared Workspace
based on email, domain or browser input. These behaviors have no separate AgentFlow
feature switches. Registration requires an IdP supporting `prompt=create`; the
redirect does not enable registration in the IdP. Disable account creation in
the IdP when sign-in should be limited to existing accounts; those verified
accounts still receive a personal Workspace on first login.

### AgentFlow Authentication Theme

The repository owns [`deploy/keycloak/themes/agentflow`](../../deploy/keycloak/themes/agentflow):
workbench palette, locally hosted IBM Plex Sans and customized English messages.
It inherits native forms, validation, required actions and scripts rather than
forking authentication templates. Tested with **Keycloak 26.7.5**; re-run the
theme gate before upgrades. Other OIDC providers can sign in, but this theme
and the tested registration behavior are Keycloak-specific.

1. Add a persistent read-only mount to your existing Keycloak deployment:
   `--mount type=bind,src=<repo>/deploy/keycloak/themes/agentflow,dst=/opt/keycloak/themes/agentflow,readonly`.
   Preserve its existing data/config when recreating the container; `docker cp`
   alone would not survive recreation.
2. Select **Realm settings → Themes → Login theme → agentflow** in the intended realm.
3. Enable **Realm settings → Login → User registration**. Password policies,
   brute-force protection, email verification/SMTP and MFA remain IdP settings.
4. Configure `AUTH_MODE=oidc` and restart the API once. Subsequent signups
   return directly to their personal Workspace, without manual Membership setup.

The design/assets belong to AgentFlow, but the HTML forms are served by Keycloak
and post **directly to Keycloak**, not Next.js or Go. The IdP hostname remains
visible; same design does not mean same hosting. Protected Next.js pages redirect
unauthenticated users directly to the themed IdP login page; account creation
is offered there by the IdP, not through an intermediate AgentFlow screen.
AgentFlow never supplies a password form or password-grant proxy. English
messages are customized; other locales inherit Keycloak's native messages.

### Database Memberships

Postgres stores both entities and grants. `workspaces.owner_user_id` is the single
ownership authority; composite foreign keys ensure `auth_memberships` cannot grant
one owner's Workspace to another user. Startup does not read member files or
import shared grants. It requires the [current Workspace schema](workspace-lifecycle.md);
ambiguous ownership is never guessed. Automatic onboarding grants only a new user's own personal space.

Operators can revoke/restore an owner's access in `auth_memberships`, referencing
the internal `auth_users.id`, not email or display name. The user's OIDC `sub`
is scoped by issuer; an authenticated nonmember sees it on the access-required
screen but cannot mount the workbench after its access has been revoked.

Operator changes to `auth_memberships`
are visible on subsequent requests and the next session probe/reload; **no API
restart required**. There are no pushed UI updates or member-management screens.
To restore revoked access, explicitly add that Membership in the DB; do not
delete its onboarding marker to trigger re-enrollment.

## Login, Session and Membership Flow

1. `GET /api/auth/login` creates a ten-minute, one-use login transaction and sets
   a host-only HttpOnly SameSite=Lax state cookie. The authorization redirect uses
   state, nonce and S256 PKCE. Explicit sign-in always sends `prompt=login` to
   require provider reauthentication, even if its SSO cookie is still valid.
   `GET /api/auth/register` reuses the flow with `prompt=create`; no password
   crosses AgentFlow. Both entry points are available in OIDC mode and return 404
   in trusted-local mode. The IdP decides whether to accept registration.
2. `GET /api/auth/callback` compares cookie/query state, atomically consumes the
   transaction, exchanges the code, and verifies ID-token signature, issuer,
   audience, expiry and nonce with `coreos/go-oidc` / `golang.org/x/oauth2`.
   This client accepts only its own audience and rejects a foreign `azp`
   (authorized party); cross-client token sharing is not supported.
3. Verified `(issuer, subject)` maps to a stable internal `user_id`. Login updates
   the display name. A transaction creates a personal Workspace entity
   record and grant only once. The record outlives grant revocation, preventing
   future logins from restoring access. Access/refresh/ID tokens are not
   retained or returned to the frontend.
4. A random 256-bit opaque session cookie references a SHA-256 hash in Postgres.
   Session lifetime is the shorter of the configured absolute TTL (1m..24h) and
   ID-token expiry. HTTPS cookies use the `__Host-` prefix and Secure attribute.
5. Each business request verifies the persisted session and current membership
   in the selected Workspace. Header/query/body scope cannot disagree. Authenticated
   default selection is also binding: a body cannot silently choose another scope.
6. `POST /api/auth/logout` revokes the session server-side and clears its cookie.
   A copied old cookie is then unusable, regardless of remaining
   `AUTH_SESSION_TTL`. The next explicit sign-in requires provider
   reauthentication. This does not revoke the provider's SSO sessions in other
   applications or the user's AgentFlow sessions on other devices.

Closing a tab does not call logout or delete the session. Reopening AgentFlow
in the same browser restores the still-valid cookie session without an OIDC
authorization redirect, up to the shorter of `AUTH_SESSION_TTL` and ID-token
expiry. The identity provider controls fresh authentication; with existing SSO
it may retain the username and ask only for the password. Full login and
password-only reauthentication both inherit the theme.

`GET /api/auth/session` returns mode, authentication state, safe user identity
and current Workspace IDs, plus an optional authorized
`personal_workspace` (the current authorized active default; retained field name
for client compatibility). Anonymous probes return 200 with no user; persistence
failures return 503, never a fallback to local mode. The frontend waits for this
probe and the authoritative `/api/workspaces` list before mounting business consumers,
redirects anonymous OIDC users directly
to `/api/auth/login`, and offers Sign out and a compact
Workspace menu displaying mutable entity names and archived status, and fully
reloads scoped application state on a switch.
Expired business requests return 401 and redirect to the same login page.
Session-probe failures and authenticated users without membership retain a local
error/access-required screen instead of redirecting repeatedly. The public home
page and local mode do not trigger this redirect. Foreign, revoked or deleted
Workspace requests return 404; invalid Origin or shared configuration writes return 403.
Selecting a Workspace is not itself a grant.

Cookie-authenticated mutations, including logout, require the exact configured
frontend `Origin`; missing or foreign Origin is rejected. Credentialed browser
CORS uses only configured origins. CLI callers using browser cookies must send
that Origin too; there is no bearer/service-account authentication in this scope.

Membership is checked at **request admission and before each SSE write**. Logout,
expiry, revocation or Workspace deletion denies further delivery with a safe
error frame; observer endpoints close and the browser stops automatic retries.
Identity expiry uses the same sign-in redirect as an HTTP 401. Detached Runs are
not canceled by losing observer access: they continue under their admitted,
frozen Workspace scope. Reconnects and execution commands require fresh access.
User-related metadata fields and operator-supplied audit labels are not grants.

## Persistence and Privacy

Startup idempotently creates `auth_users`, `auth_memberships`, `auth_sessions`,
`auth_login_attempts`, `auth_personal_workspaces` and `workspaces`.
The personal Workspace record preserves onboarding history and default selection,
not passwords or roles. Its nullable reference preserves revocation history even
when access to its personal Workspace is removed.
Old file-import bookkeeping is no longer created, required or accessed; any
existing unused table is left untouched rather than dropped during startup.
Startup refuses ambiguous or unmigrated references; one-time legacy migration
commands are not part of the current runtime.
Sessions survive restart, logout removes them, and expired session/transaction
rows are purged opportunistically on the next session/login creation. There is
no idle refresh, background cleanup scheduler or provider-wide session revocation.

Session/state bearer values are not stored in plaintext. A pending login briefly
stores its nonce and PKCE verifier in the restricted transaction table until it
is consumed or expires. The client secret is resolved only for OAuth transport
construction, not serialized Config or Runtime Snapshot. Authentication records
never enter model Context, Events, Capture or Replay. The configured issuer,
opaque subject and display name are identity data retained in Postgres; include
them in the deployment's retention/backup/access controls. Errors expose stable
messages, not upstream token payloads. Operators must not enable proxy/access
logging of cookies, callback codes or tokens. Add edge login-rate protection
before an untrusted deployment; this feature does not add a public identity service.

## Repeatable Validation

Use the required Go version and a dedicated `TEST_DATABASE_URL` with CREATEDB
privileges; tests create/drop disposable databases, never the application data:

```bash
cd apps/api
go test ./internal/identity ./internal/httpapi \
  -run 'TestOIDC|TestAuthentication|TestAuthenticatedWorkspace|TestPersonalWorkspace|TestIdentitySchema|TestDatabaseMemberships' \
  -count=1 -v
```

From the repository root, the narrow browser gate is:

```bash
AGENTFLOW_IDENTITY_TEST=1 bash scripts/test-browser.sh identity.spec.ts
```

It uses production Go composition, Next.js and Postgres with a signed OIDC
fixture enforcing actual discovery, JWKS, authorization and PKCE exchange.
`identity-evidence.json` and `nonmember-evidence.json` are retained as Playwright
attachments under the ignored test-results/report directories, without cookies
or provider tokens. Backend tests log an `identity_evidence`/`identity_boundary_evidence`
summary and exercise invalid tokens/configuration, state replay, storage failure,
restart, revocation, and rejection before writes. This is protocol and boundary
evidence, not a live-provider deployment test or proof of complete PROD-002 ACLs.

The real themed registration gate additionally requires Docker:

```bash
bash scripts/test-keycloak-auth.sh
```

It creates a disposable Keycloak **26.7.5** realm on loopback port 19081 with
the theme. Native registration/password policy, wrong-password rejection,
personal-only access, conversation reload, logout and repeat login are checked
through real Next.js/Go/Postgres. The test container is removed on exit; operator
accounts/realms are untouched. `onboarding-evidence.json` retains results and
limitations, not cookies/passwords. A cached image can be selected through
`KEYCLOAK_TEST_IMAGE`; the runner checks its version. This is not full MFA/reset
coverage, a production-configuration audit or proof of complete object ACLs.

## Rollback and References

Reverting the feature removes authentication enforcement. Do not switch to local
mode or revert on an exposed deployment without restoring an external access
boundary. Added tables can remain; no destructive database rollback is needed.

To stop new account creation, disable registration at the IdP; existing users
can still sign in and receive their personal Workspace. There is no separate
AgentFlow onboarding switch. After the Workspace identity migration, a pre-lifecycle
binary is not a safe standalone rollback: it assumes legacy global defaults and
does not enforce archive/deletion. Use a matching full database backup and binary
for a downgrade, with traffic stopped. See [Workspace lifecycle](workspace-lifecycle.md).

- [Go OIDC client](https://github.com/coreos/go-oidc)
- [Go OAuth2 client and PKCE options](https://pkg.go.dev/golang.org/x/oauth2)
- [OpenID Connect ID-token validation](https://openid.net/specs/openid-connect-core-1_0.html#IDTokenValidation)
- [OpenID Connect reauthentication request](https://openid.net/specs/openid-connect-core-1_0.html#AuthRequest)
- [OWASP session management](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)
- [OWASP authorization](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html)
- [Keycloak theme inheritance](https://www.keycloak.org/ui-customization/themes)
- [Keycloak registration and prompt=create](https://www.keycloak.org/docs/latest/server_admin/#_user-registration)
