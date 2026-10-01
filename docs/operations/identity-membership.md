# Identity and Workspace Membership

OIDC identity, AgentFlow-owned login/registration presentation and personal
Workspace onboarding. Passwords stay at the identity provider; this is not a
complete account-management product or the object-authorization audit in PROD-002.

## Failure Inventory

| Failure | Required outcome |
| --- | --- |
| Missing, forged, revoked or expired session | 401 before any business handler executes. |
| Valid identity without selected Workspace membership | 403; never auto-enroll. |
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
| Authentication disabled for trusted local use | Existing namespace and API behavior stay unchanged. |
| Duplicate or concurrent first login | One personal Workspace and one Membership; existing grants remain unchanged. |
| Personal Workspace grant fails | Marker and grant roll back together; no new Session; a fresh login retries. |
| Membership revoked after onboarding | Relogin/restart does not restore it; empty Membership is not a new-account signal. |
| Legacy file changed or stale on upgrade | Database remains authoritative; no removal or restoration of grants. |

## Modes and Scope

`AUTH_MODE=local` is the default for trusted development. It preserves existing
Workspace namespace selection and establishes **no authenticated identity**.
Do not expose it to untrusted users. `AUTH_MODE=oidc` enables login, revocable
sessions and server-side membership checks before every business request,
including SSE, Replay, Artifact, Memory and knowledge endpoints. Only health,
the five authentication endpoints and CORS preflight are exempt.

Registration uses the IdP's native account creation through `prompt=create`;
AgentFlow adds only an opt-in personal namespace and Membership. No local password
storage, password proxy, invitations, roles, per-user object ACLs, general Workspace
administration or service-account tokens. Agent/Tool configuration remains shared operator
configuration; all members currently have the same access. Object ownership and
the complete cross-tenant authorization audit remain PROD-002. This boundary alone
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
AUTH_AUTO_PROVISION_WORKSPACE=true
AUTH_REGISTRATION_ENABLED=true
AUTH_MEMBERSHIP_PATH=
AUTH_SESSION_TTL=8h
```

Issuer, discovery endpoints, callback and frontend require HTTPS, except on
loopback. Frontend and API must share the **same scheme and hostname**; local
development can use different ports. Deploy the API behind the same HTTPS origin
as Next.js or use an equivalent reverse-proxy arrangement. Set
`NEXT_PUBLIC_API_BASE_URL` to the browser-facing API origin (empty means same-origin)
when building the web application. Do not mix `localhost` and `127.0.0.1`.

Both new flags default to `false`, preserving existing manual-access deployments.
With onboarding enabled, a verified first login (including an existing IdP account)
grants **only that identity's own personal Workspace**, never a shared Workspace
based on email, domain or browser input. Registration requires onboarding and
an IdP supporting `prompt=create`; it does not enable registration in the IdP.

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
4. Enable both AgentFlow flags and restart the API once. Subsequent signups
   return directly to their personal Workspace, without editing member JSON.

The design/assets belong to AgentFlow, but the HTML forms are served by Keycloak
and post **directly to Keycloak**, not Next.js or Go. The IdP hostname remains
visible; same design does not mean same hosting. Protected Next.js pages redirect
unauthenticated users directly to the themed IdP login page; account creation
is offered there by the IdP, not through an intermediate AgentFlow screen.
AgentFlow never supplies a password form or password-grant proxy. English
messages are customized; other locales inherit Keycloak's native messages.

### Legacy Membership Bootstrap

For a new issuer needing initial shared grants, optionally set
`AUTH_MEMBERSHIP_PATH` to an operator-owned regular JSON file, at most 64 KiB:

```json
{
  "members": [
    { "subject": "<provider-subject>", "workspaces": ["default_workspace"] },
    { "subject": "<another-provider-subject>", "workspaces": ["project-workspace"] }
  ]
}
```

Subjects must be the provider's opaque OIDC `sub` claim, not email addresses or
display names. Obtain it from the provider's administration interface or sign in:
an authenticated nonmember sees their subject and an access-required screen but
cannot mount the workbench when onboarding is disabled or its grant was revoked.
Keep real grant files under ignored `.data/`,
not in version control. `{ "members": [] }` intentionally grants nobody access.

Startup validates a configured file and consumes an atomic, **one-time per issuer**
import. File changes never remove, update or regrant Membership. If the issuer
already has users (including an upgrade from the previous implementation), its
database grants are preserved and the stale file is not imported. Existing
deployments can clear `AUTH_MEMBERSHIP_PATH`; an empty path needs no file.

Postgres is the Membership authority. Operator changes to `auth_memberships`
are visible on subsequent requests and the next session probe/reload; **no API
restart required**. There are no pushed UI updates or member-management screens.
To restore revoked access, explicitly add that Membership in the DB; do not
delete its onboarding marker or replay a legacy import.

## Login, Session and Membership Flow

1. `GET /api/auth/login` creates a ten-minute, one-use login transaction and sets
   a host-only HttpOnly SameSite=Lax state cookie. The authorization redirect uses
   state, nonce and S256 PKCE. Explicit sign-in always sends `prompt=login` to
   require provider reauthentication, even if its SSO cookie is still valid.
   `GET /api/auth/register` reuses the flow with `prompt=create`; no password
   crosses AgentFlow. A disabled register route returns 404; enabling registration
   without automatic provisioning refuses startup.
2. `GET /api/auth/callback` compares cookie/query state, atomically consumes the
   transaction, exchanges the code, and verifies ID-token signature, issuer,
   audience, expiry and nonce with `coreos/go-oidc` / `golang.org/x/oauth2`.
   This client accepts only its own audience and rejects a foreign `azp`
   (authorized party); cross-client token sharing is not supported.
3. Verified `(issuer, subject)` maps to a stable internal `user_id`. Login updates
   the display name. If enabled, a transaction creates a personal namespace
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
and current Workspace IDs, `registration_enabled` and an optional authorized
`personal_workspace`. Anonymous probes return 200 with no user; persistence
failures return 503, never a fallback to local mode. The frontend waits for this
probe before mounting business consumers, redirects anonymous OIDC users directly
to `/api/auth/login`, and offers Sign out and a compact
Workspace menu (personal namespace labeled "Personal workspace"), and fully
reloads scoped application state on a switch.
Expired business requests return 401 and redirect to the same login page.
Session-probe failures and authenticated users without membership retain a local
error/access-required screen instead of redirecting repeatedly. The public home
page and local mode do not trigger this redirect. Nonmembership returns 403.
Selecting a Workspace is not itself a grant.

Cookie-authenticated mutations, including logout, require the exact configured
frontend `Origin`; missing or foreign Origin is rejected. Credentialed browser
CORS uses only configured origins. CLI callers using browser cookies must send
that Origin too; there is no bearer/service-account authentication in this scope.

Membership is checked at **request admission**. Existing streams and detached
Runs are not retroactively stopped by logout or membership removal; subsequent
requests/reconnects are checked again. User-related data fields in existing
resources are not newly converted into owner ACLs by this feature.

## Persistence and Privacy

Startup idempotently creates `auth_users`, `auth_memberships`, `auth_sessions`,
`auth_login_attempts`, `auth_personal_workspaces` and `auth_membership_imports`.
The latter two preserve onboarding/import history, not passwords or roles.
Existing resource rows are not moved or assigned to new owners.
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
  -run 'TestOIDC|TestAuthentication|TestAuthenticatedWorkspace|TestPersonalWorkspace|TestMembershipImport' \
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

An onboarding-only rollback disables the two flags and retains grants/records.
Do not roll back to a binary that synchronizes a stale member file: it would
replace database grants. Back up auth tables and clear that file configuration
before such a rollback.

- [Go OIDC client](https://github.com/coreos/go-oidc)
- [Go OAuth2 client and PKCE options](https://pkg.go.dev/golang.org/x/oauth2)
- [OpenID Connect ID-token validation](https://openid.net/specs/openid-connect-core-1_0.html#IDTokenValidation)
- [OpenID Connect reauthentication request](https://openid.net/specs/openid-connect-core-1_0.html#AuthRequest)
- [OWASP session management](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html)
- [OWASP authorization](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html)
- [Keycloak theme inheritance](https://www.keycloak.org/ui-customization/themes)
- [Keycloak registration and prompt=create](https://www.keycloak.org/docs/latest/server_admin/#_user-registration)
