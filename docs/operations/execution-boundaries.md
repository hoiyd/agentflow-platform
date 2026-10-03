# Execution Boundaries

PROD-004 hardens existing online execution surfaces. Tool Policy grants a
logical capability; it does not replace the checks at HTTP dialing, file
opening, or process creation. This is not a general OS sandbox.

## Failure Inventory

These cases define the implementation and regression checks:

| Surface / failure | Expected outcome |
| --- | --- |
| HTTP origin absent from operator allowlist, including loopback | `blocked`, `policy_denied`; no destination request |
| Wrong scheme/port, URL credentials, unsupported method | Rejected before network access |
| Redirect, even to another allowed origin | `blocked`, `policy_denied`; no redirected request |
| Public DNS name resolves to private, loopback, link-local or mixed addresses | Denied before dialing; validate all A/AAAA answers |
| DNS changes between validation and connection | Connect only to the validated IP; no second lookup |
| Environment HTTP proxy is set | Do not use it for governed egress |
| Cancellation, timeout, broken or oversized response | Bounded failure, never implicit verification success |
| Binary output contains NUL/invalid UTF-8 | Sanitize retained text, preserve observed byte count/hash; Evidence persists in Postgres |
| Remote/OIDC caller requests an allowlisted host command | `blocked`, `policy_denied`; no process is started |
| Trusted local command uses escaping/symlinked cwd or relative executable | Rejected before process creation |
| Trusted local command inspects inherited provider/OIDC credentials | Credentials absent; child environment is explicitly constructed |
| Trusted local command times out with subprocesses | Terminate its process group on supported platforms; bound pipe wait |
| Skill resource is a symlink, escapes its opened root, or exceeds limits | Existing Loader rejects it; no shell or arbitrary file access |

## Scope

HTTP Verifier and Tavily use governed egress. Operator-configured model/OIDC
endpoints deliberately remain separate: local inference and self-hosted identity
are supported destinations, not user-selected URLs. Skills retain their existing
`os.Root` read boundary. Future file writes, MCP execution and third-party scripts
must establish their own actual boundary; no unrestricted fallback is permitted.

Destination allowlisting, redirect rejection and dial-time DNS validation follow
the [OWASP SSRF prevention guidance](https://cheatsheetseries.owasp.org/cheatsheets/Server_Side_Request_Forgery_Prevention_Cheat_Sheet.html).
Deployment network controls remain defense in depth; application checks are not
kernel-level isolation.
The conservative special-purpose IP exclusions are based on the
[IANA IPv4](https://www.iana.org/assignments/iana-ipv4-special-registry/) and
[IPv6 registries](https://www.iana.org/assignments/iana-ipv6-special-registry/).
`IsGlobalUnicast` alone is not proof of public routability; special/translation
ranges remain denied even if an operator lists them as an origin.

## Operator Configuration

- `VERIFICATION_ALLOWED_HTTP_HOSTS`: exact origins; empty denies all destinations.
  Bare public hostnames grant HTTPS:443 only. Private/loopback access needs an
  explicit origin with a literal IP or `localhost`; hostname DNS cannot grant
  access to private addresses. Link-local metadata and reserved addresses remain
  denied. Every origin grant includes its paths, so grant only trusted read-only
  services, never admin or metadata endpoints. An explicit private origin is a
  privileged operator grant, not proof that the destination is safe. Redirects
  are always denied.
- HTTP checks use GET/HEAD only, no authorization headers, no inherited proxies,
  <=10 seconds total and <=1 MiB body plus one detection byte. Retained output
  additionally obeys `VERIFICATION_MAX_ARTIFACT_BYTES`. A truncated network read
  never produces a full-body hash claim or passes on status alone.
- Tavily uses the same dial policy for its fixed HTTPS origin and sends its
  own scoped credential only to `/search`; it cannot select another destination.
- Host commands require `AUTH_MODE=local`, a configured root and absolute
  executable allowlist. Children receive only fixed PATH/locale values, never
  inherited service credentials. Linux/macOS process groups are terminated on
  cancellation and after execution, with bounded pipe wait. Other platforms deny.

Canonical cwd checks prevent accidental root escapes but are **not** a file
sandbox or a race-proof capability for hostile scripts. Local mode trusts the
operator and must not be exposed to untrusted callers. Programs can still read
host files (including configuration), write outside cwd, use the network and
deliberately escape process groups. OIDC mode denies host commands before exec;
[The sbx command runner](sandbox-execution.md) supplies a separate, opt-in
mountless execution boundary. It does not make this host Command Verifier
sandboxed or authorize Skill scripts, mounts or third-party integrations.
Skill resources already use `os.Root`, regular-file and symlink checks, text
limits and frozen content; they do not inherit command permissions.

Current operator policy is checked on every execution, including re-verification
of old contracts. There is no database migration or implicit grant for old
loopback URLs. Existing JSON/event/Evidence shapes remain unchanged; denied
checks use the existing `blocked` status and `details.reason_code` in Replay.

## Repeatable Evidence

```bash
# Dedicated test database only; never the operator's DATABASE_URL.
export TEST_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:15433/agentflow_test?sslmode=disable'
cd apps/web
AGENTFLOW_EXECUTION_BOUNDARY_TEST=1 npm run test:e2e -- execution-boundaries.spec.ts
```

The focused gate uses a signed OIDC fixture, production application composition,
real local destination servers and disposable Postgres. It verifies an allowed
HTTP check, denied port, forbidden redirect and oversized binary response,
then reloads persisted Evidence in Run Replay. A deliberately allowlisted host
command is rejected both in the UI and through direct authenticated HTTP;
destination hit counters and a command marker prove no prohibited action ran.
`execution-boundary-evidence.json` records contracts, Run identities, Evidence,
operator grants, observations and limits in `apps/web/test-results` and CI
artifacts. It proves fixture behavior, not live provider quality or OS isolation.
Backend tests separately cover mixed/private DNS answers, IP-pinned dialing,
proxy exclusion, cancellation, environment credentials and subprocess cleanup;
existing Skill tests cover the root boundary. No full browser suite is needed.
