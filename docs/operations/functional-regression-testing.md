# Functional Regression Gates

Browser gates exercise the production Next.js client, Go `app.New`, Runtime,
registered Bindings and disposable Postgres. External model/embedding transports
are controlled fixtures; business persistence and successful API responses are
not fabricated. Test-only control routes never enter `cmd/server`.

## Run and Evidence

Use Go 1.26.5, Node 22+, installed frontend dependencies and Chromium. From the
repository root, supply a dedicated pgvector database with `CREATEDB` privileges:

```bash
export TEST_DATABASE_URL='postgres://test-user:test-password@127.0.0.1:5432/agentflow_test?sslmode=disable'
npm --prefix apps/web ci
npm --prefix apps/web exec playwright install chromium
bash scripts/test-browser.sh runtime.spec.ts --grep 'Tool-enabled answer'
```

Each composition creates/drops its own database; the database in the URL is not
migrated or cleared. Ports 13000/18080 must be free; existing services are never
reused. `.next-functional` keeps production test builds separate from development.
Wrappers restore Next-generated source files, including on failure. Do not run
builds concurrently in one checkout. Shell entry points load `scripts/.env`,
never the application `.env`; process overrides win. See [configuration](backend-configuration.md#configuration-files).

CI runs `npm --prefix apps/web run test:e2e:ci`. The profile/file/flag assignment
has one owner: [`browser-gates.mjs`](../../scripts/browser-gates.mjs). One frontend
build is reused by matching build ID; each profile starts fresh servers and
Postgres. Usage/reasoning share a route-evidence profile; managed writes and
planning keep separate file state. Identity, approval and other policy profiles
remain isolated. Keycloak uses its disposable container, never the operator IdP.

For a small multi-profile check, select names rather than running the full suite:

```bash
bash scripts/test-browser-ci.sh planning tool-approval
node --test scripts/browser-gates.test.mjs scripts/script-env.test.mjs
```

CI stores each profile's JSON results, attachments, HTML report and failure traces
under `apps/web/test-results/gates/NAME/`; `summary.json` records revision, build
count/ID, flags, duration and executed/skipped/failed counts. Output is cleared
before building. A failed profile does not stop independent later profiles;
build failure or cancellation leaves remaining profiles `not_run`. Missing
results or an all-skipped profile cannot pass. Artifacts are ignored by Git and
retained by CI for 14 days. Standalone Playwright runs retain `test-results/`
and `playwright-report/`; skipped cases alone are not acceptance evidence.

## Failure Inventory

Before adding a fixture, state the failure and expected durable/UI outcome in
the owning test. Keep the detailed matrix with that test or its feature contract,
not in a second cross-project checklist.

- Stub external transport, not Tool descriptors, authorization, Store or business behavior.
- Give each case its own Conversation, owned Agent and explicit Tools/routing signals.
  Do not depend on earlier cases, shared names or global templates.
- Control races with a request/response handshake, not fixed sleeps, retries or
  larger timeouts. Delay real API responses rather than fabricate successful writes.
- Assert pre-write rejection and durable failure state, not returned errors alone.
  Cover reload, stale input and the affected runtime modes without inventing Stage IDs.
- A completed Run may receive later title/Memory telemetry. Compare the observed
  immutable event prefix by sequence, then verify allowed appends by request identity.
- Keep command acceptance, browser observation, cancellation and late-navigation
  results separate. A stale callback must not modify the selected Run or draft.
- Record identity, synthetic inputs, frozen configuration, outcomes and limitations.
  Use screenshots for visible changes, not as substitutes for functional assertions.

## Gate Index

The [profile source](../../scripts/browser-gates.mjs) owns exact selections and
flags. These links own behavior, failure semantics and focused operating commands.

| Boundary | Owning contract / evidence |
| --- | --- |
| Tool continuation, corrections, real answer streaming | [Bounded Tool loop](../tools/bounded-tool-loop.md#failure-inventory-and-test-plan), [`runtime.spec.ts`](../../apps/web/e2e/runtime.spec.ts) |
| Clarification and same-Run recovery | [Structured clarification](../runtime/structured-clarification.md#api-and-recovery) |
| Exact-call Tool approval | [Tool approval](../tools/tool-approval.md#api-and-evidence) |
| Read-only plan and separate execution | [Planning](../runtime/read-only-planning.md#validation-and-rollback) |
| Inbox, steering and compaction/budget boundaries | [Durable inputs](../runtime/durable-inputs.md#verification) |
| Knowledge scope, source revocation and edits | [Runtime filters](../knowledge/runtime-knowledge-filters.md#verification), [document lifecycle](../knowledge/document-lifecycle.md#verification) |
| Filesystem reads, managed writes and Undo | [Reads](../tools/scoped-filesystem.md#validation), [writes](../tools/scoped-filesystem-writes.md#validation-and-rollback), [Undo](../tools/file-undo.md) |
| Web source acquisition | [Web fetch](../tools/web-fetch.md#verification) |
| Project instructions | [Scoped instructions](../context/project-instructions.md#verification) |
| Skill invocation/readiness and package review | [Invocation](../tools/skill-invocation.md#evidence), [review](../tools/skill-package-review.md) |
| Structured output and Completion Gate | [Structured output](../runtime/structured-output.md#verification), [Verification](../runtime/verification.md) |
| Usage, reasoning and partial output | [Budget](../runtime/run-budget.md), [reasoning](../runtime/provider-reasoning.md), [partial recovery](../runtime/durable-partial-output.md) |
| Host/network/sandbox enforcement | [Execution boundaries](execution-boundaries.md#repeatable-evidence), [sandbox commands](sandbox-execution.md) |
| Headless HTTP/SSE client | [Headless Run client](../runtime/headless-run-client.md#verification) |

## Configuration Upgrade Gate

The [Failure Corpus](../evaluation/failure-corpus.md#historical-seeds-and-limits)
owns the promoted startup regression and its exact invocation. It uses a fixed
[prior policy](../../apps/api/app/testdata/tools.pre-web-fetch.json) through real
`app.New`, HTTP, Sandbox Binding and Postgres. Never regenerate that fixture from
current defaults or relax validation to make it start. Retain permissions,
rejection, receipts, restart, revocation and invalid-startup cleanup evidence.

## Trusted MCP

The [MCP adapter](../tools/mcp-adapter.md) owns operator and Workspace-connection
contracts. Separate profiles check discovery/readiness, authenticated selection,
all-mode calls, frozen-definition drift, disable/delete and historical Replay.
Controlled SDK transport is not evidence of external service availability.

## Tool Discovery

The [discovery contract](../tools/tool-discovery.md) owns lazy-schema activation,
unloaded-call denial, frozen visibility and restart checks. Its browser profile
uses real guarded Bindings and rejects missing next-request schemas.

## Owner Model Admission

The [control map](../runtime/execution-controls.md#owner-scoped-admission) owns limit units
and precedence. [`owner-admission.spec.ts`](../../apps/web/e2e/owner-admission.spec.ts)
checks distinct owners, same-owner Workspace competition, spoof rejection, Stop
permit release and staged Tool rounds. Queue/timeout/retry/close failures remain
with the focused Go tests; the browser profile does not claim serving throughput.

## Workspace Configuration

[Agent/Tool ownership](../architecture/workspace-agent-tools.md) and
[resource authorization](resource-authorization.md) own scoped CRUD, grants,
revocation and cross-owner rejection. [Identity validation](identity-membership.md#repeatable-validation)
owns login/session/onboarding. Picker geometry and keyboard behavior remain in
the existing Agent-selection gate; Keycloak password forms have a separate gate.

## Test Boundaries

Component/library and backend tests still own races, parsers, schema rejection,
privacy, uncertain writes and crash windows that browser happy paths cannot
prove. Do not delete them just because a browser gate exists, or add fixture-echo
tests merely for coverage. Reviewed historical incidents and promotion evidence
belong in the [Failure Corpus](../evaluation/failure-corpus.md).

Controlled model/embedding results prove protocol, plumbing and persistence,
not semantic quality, live availability or actual network egress. Controlled sbx
CLI evidence is not VM-isolation evidence. Process termination, production
capacity and model quality have separate [drill](release-recovery-drill.md) and
[evaluation](../evaluation/README.md) owners. Run only the smallest affected scope
during development; the complete profile set remains a CI gate.
