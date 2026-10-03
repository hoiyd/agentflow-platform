# Functional Regression Gates

These gates protect user-visible behavior, not coverage percentages. The two
Tool regressions exposed a missing layer: component tests mocked the API, while
direct Tool-loop tests did not exercise the production application composition.

## Failure Inventory

Write or update this inventory before adding isolated fixtures.

| Failure | Expected observation |
| --- | --- |
| Thinking continuation fields are lost between Tool rounds | The strict fixture rejects the actual follow-up HTTP request; the browser test fails, not silently succeeds |
| Internal Task State writes are classified as external writes | Single Chat fails its real guarded write without an invented Stage ID |
| Invalid arguments cannot be corrected | Schema error reaches the next model request; only the corrected operation persists |
| Stale Task State version overwrites facts | Real HTTP patch returns 409; state and revision count remain unchanged; Tool-level rejection/uncertain settlement retain their focused integration tests |
| Skill is configured but never loaded | Missing body sentinel in the subsequent request fails the provider contract |
| Tool-mode answers are buffered until completion | Browser must see a partial answer before it releases the provider's completion gate |
| A provider failure looks successful or remains loading | Failed status and useful error are visible and survive Replay |
| Verification UI never sends a contract or completion bypasses the gate | Browser-submitted policy is persisted; a failing verifier prevents `completed` |
| Multi/Loop execution loses Tool continuation or stage identity | Real staged runs finish with paired calls and a valid projection; Loop writes have durable receipts; isolated Multi Worker has no Task State write authority |
| Reload loses accepted results or persisted Task State | Reload reads the same messages, run, and Task State from PostgreSQL |
| Tool toggles only update local UI state | Catalog API and reloaded page agree on the persisted enabled flag |
| Knowledge ingestion/search/deletion disagree | Real index detail and retrieval show the inserted document; deletion returns 404 |
| Memory corrections/deletions lose version or remain recalled | History records the correction; version increases; deletion clears content and recall |
| Two command callbacks fire before a React render | Only one command is admitted and one optimistic message pair is created |
| A stream fails before or after accepting events | Before acceptance, drafts roll back; after acceptance, partial output stays and durable observation resumes |
| A frozen Skill is mistaken for an observed model input | Replay joins Manifest with the physical request record; missing evidence remains not observed |
| Login or Workspace selection is mistaken for permission | The signed OIDC/Postgres browser gate proves membership, nonmember rejection, scoped reload and server-side logout revocation |
| A valid identity guesses another owner's object ID or supplies linked evidence | The resource authorization gate rejects reads, mutation and Memory provenance; owned resources and persisted revisions remain unchanged |
| Production wrapping discards scoped Artifact reads | Artifact list/read/search retain capability and authorization in the real application composition |
| Access is revoked after a stream was admitted | Further delivery stops, the browser reauthenticates, and the durable Run still completes independently |
| Signup styling bypasses IdP security or creates shared access | The disposable Keycloak theme gate proves native password validation, direct IdP form submission, personal-only access, reload and repeat-login stability |
| A late Cancel response follows a terminal stream event | It cannot regress the Run to `canceling` or revive cancellation UI |
| Stop cancels an in-flight model request | Single, Multi-Agent Continue and Loop return `done:canceled`, not SSE `error`; the canceled Run survives reload, partial reasoning remains withheld, and the composer accepts another task |
| Earlier cases leave conversation titles containing mode labels | Mode selection is scoped to the named Chat mode region, never the sidebar's conversation or delete buttons; run the affected gate in CI order, not only the new case |
| Earlier cases leave Workers with the same routing capability | Each scenario declares and requests its own capability; exercise the real Router without weakening score-margin gates to bypass fixture collisions |
| Navigation occurs during execution, cancellation, or observation | Local requests detach; their late events, errors and snapshots cannot change the new conversation |
| Observed historical events precede the canonical snapshot | Stage details rebuild without regressing the snapshot's current Run status; stopped Runs reload persisted messages once |

## Execution Boundary

The opt-in browser gate exercises real HTTP destination enforcement and OIDC
command denial through production composition and disposable Postgres. It
retains contract, Evidence and destination hit counters, and verifies Replay
reload. DNS and process failure checks run separately without external services.
See [Execution boundaries](execution-boundaries.md#repeatable-evidence) for the
failure inventory, command and evidence limitations.

## Owner Model Admission

The focused gate uses two signed OIDC identities, the production composition,
and disposable Postgres. One identity holds a gated streaming response, then
attempts another Run in a second owned Workspace. The second identity must still
complete; spoofing `X-Owner-ID` cannot bypass the first identity's capacity.
Stop releases the physical connection; a subsequent Run and background Memory
sync succeed, and owner-wait diagnostics survive Replay/reload.
Separate Single, Multi-Agent, and Bounded Loop cases each complete four real
Tool rounds with owner capacity one, verifying permit reuse and persisted
owner-wait diagnostics for every physical model attempt.

```bash
AGENTFLOW_OWNER_ADMISSION_TEST=1 AGENTFLOW_IDENTITY_TEST=1 \
  TEST_DATABASE_URL='postgres://user:password@127.0.0.1:5432/agentflow_test?sslmode=disable' \
  npm --prefix apps/web run test:e2e -- owner-admission.spec.ts
```

The fixture fixes global=2, owner=1, owner queue=0, owner wait=3s. Its JSON
attachment records identities, Workspace IDs, limits, Runs, persisted events,
checks, and limitations. Deterministic Go tests additionally cover nonzero
waiting queues, cancellation/timeout/close in owner/key/global phases, retries,
and durable ownership after revoked access. This proves process-local control
semantics, not provider throughput or strict fairness.

## Test Boundaries

Browser E2E uses the real Next.js workspace, browser API client, Go production
composition (`app.New`), runtime, registered Bindings, guards, and a disposable
Postgres database. External model/embedding and, in the identity gate, OIDC
provider endpoints are deterministic transport fixtures.
There is no `page.route` API interception or fake business Store. Fixture control
endpoints exist only in the opt-in Go test, never in `cmd/server`.

The [identity gate](identity-membership.md#repeatable-validation) runs separately
with `AGENTFLOW_IDENTITY_TEST=1` so the ordinary trusted-local runtime suite remains
unchanged. CI retains its protocol/boundary attachments separately from runtime
evidence. This gate does not claim live-provider or complete object-ACL validation.

`AGENTFLOW_IDENTITY_TEST=1 bash scripts/test-browser.sh resource-authorization.spec.ts`
adds real signed identities and Workspace/object isolation, retrieval content,
rejected writes, reload and active Chat logout/continuity. Its JSON attachments
record resource identities and limitations. Populated Artifact/effect/Capture
and additional revocation/storage failures have focused Postgres integration tests;
see the [authorization matrix](resource-authorization.md). No screenshots are used.

`bash scripts/test-keycloak-auth.sh` additionally checks real themed authentication
with a disposable Keycloak realm and independent Postgres. It is a separate narrow
gate, not part of the trusted-local runtime suite, and retains `onboarding-evidence.json`.
It never configures an operator's IdP and does not cover every MFA/reset flow.

Focused unit/component tests remain valuable for races, parsers, schema rejection,
privacy, and uncertain-write settlement. A mocked success response is not evidence
of cross-layer correctness. Do not delete these boundary checks merely because
browser tests exist, or add struct-round-trip tests to inflate patch coverage.
Two API fixture-echo tests (Skill hash passthrough and Runtime Snapshot passthrough)
are replaced by real catalog metadata/privacy and persisted Snapshot checks.
Nullable collection normalization, request-policy, SSE parser, state-race, and
Tool uncertain-write tests remain; they cover different failure mechanisms.
The existing Task State executor integration now uses the real Single boundary
(no Stage ID) and asserts that a stale patch leaves both state and revisions
unchanged. Browser scenarios also fail on unhandled page exceptions.

The gates were checked against two temporary regression canaries, then the
production code was restored: dropping Tool continuation `reasoning_content`
fails the Single browser case with an explicit provider contract rejection;
classifying Task State as an external write fails the real executor integration
at the security boundary. Neither canary is a permanent runtime switch or an
alternative implementation.

Deterministic provider evidence does **not** establish live model quality,
provider availability, real Tavily egress, or recovery from process termination.
Those have separate owned integration/evaluation/drill suites.

The fixture returns fixed embeddings. Knowledge/Memory cases prove plumbing,
index compatibility, mutation and UI contracts, **not** semantic recall quality.
Every runtime scenario starts a new conversation; Multi-Agent uses explicit
declarative routing signals rather than changing routing thresholds or bypassing
the selection gate. Provider failures retain the safe generic 5xx message;
the UI now displays the already-supplied error source/code and request ID.

## Run and Evidence

With Go 1.26.5, Node 22+, frontend dependencies and Chromium installed:

```bash
cd apps/web
npm ci
npx playwright install chromium
export TEST_DATABASE_URL='postgres://test-user:test-password@127.0.0.1:5432/agentflow_test?sslmode=disable'
npm run test:e2e -- --grep 'Single agent'
```

The dedicated test database must exist and allow `CREATEDB`; pgvector must be
available. Each invocation creates and drops a new database via `pgfixture`,
without migrating or clearing the database in the URL. Missing prerequisites
fail the browser command instead of producing a green skipped test suite.
Ports 13000 and 18080 must be free; existing services are never reused.
The Next production build uses `.next-functional`; generated source files are
restored by the wrapper, even on failure. Do not run builds concurrently in the
same checkout.

CI runs the compact cross-layer suite as a separate gate. Local development
should select the affected scenario; `npm test` remains fast component/library
testing. `test-results/results.json`, per-test `runtime-evidence` attachments,
provider contract counters, and the HTML report retain identities, inputs,
frozen configuration, persisted outcomes and limitations. Failure traces use
only synthetic fixture data; no real provider credentials or operator Skills
are loaded. Reports are ignored by Git and retained by CI for 14 days.

See [Playwright webServer](https://playwright.dev/docs/test-webserver) for managed
process lifecycle and [retrying assertions](https://playwright.dev/docs/test-assertions)
for condition-based waiting. No fixed sleeps or automatic retries hide failures.
