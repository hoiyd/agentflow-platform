# Resource Authorization

## Boundary and Failure Inventory

OIDC identity comes from a persisted, verified session. Workspace access requires
both ownership and current Membership; resource IDs never grant access. All
user-owned operations use the selected Workspace's Store or an explicitly scoped
Memory/Knowledge provider. Local mode is trusted development as `super`, not a
multi-user authentication mechanism.

| Failure | Required outcome |
| --- | --- |
| Anonymous, forged or expired session | 401 before business work |
| Foreign, revoked or deleted Workspace | 404 without revealing its resources |
| Foreign object ID inside an accessible Workspace | 404; no reads or writes to that object |
| Same owner selects another Workspace | Same isolation as another owner |
| Header, query or body disagrees with authorized scope | 400 before provider calls or writes |
| Memory links a foreign Conversation, Run or source Message | 404 before embedding/persistence; own but inconsistent links are rejected |
| Artifact/effect belongs to another Run | 404 even when both Runs are accessible |
| Production Store decorator loses scoped Artifact capabilities | Owned reads still work; inaccessible objects return 404, not an availability fallback |
| Logout, session expiry, Membership removal or Workspace deletion during SSE | Stop resource delivery on the next write, with a safe error frame |
| Identity/authorization storage fails | Fail closed; no fallback to local identity |
| OIDC user changes shared Agent profiles or Tool switches | 403, regardless of selected Workspace |
| Archived Workspace receives a mutation or execution command | 409; read-only retrieval remains available |

Authorization does not revoke an already-admitted durable Run. Closing or denying
its observer prevents further delivery, not execution; explicit Cancel remains a
separate authorized operation. Access checks cannot retract data already sent.

The scope does not include private Agent profiles, per-Workspace Tool allowlists,
team roles, service accounts, OS sandboxing or per-document sharing. Agent, Tool
and trusted Skill configuration remain shared service configuration.

## Resource Matrix

| Surface | Authority |
| --- | --- |
| Workspace CRUD/default selection | Verified owner + current Membership; soft-deleted entities are inaccessible |
| Conversation/messages, Chat, Task State/revisions | Conversation lookup in the authorized scope before reading or mutating |
| Run, Continue/Resume/Cancel/Verify, Replay, projection, attention, usage, episode, Capture | Scoped Run lookup; derived lists inherit its scope |
| Artifact list/read/search and effect reconciliation | Scoped Run, then exact Run/artifact or Run/effect association |
| Memory create/recall/detail/mutation | Bound Workspace; optional Run derives its Conversation, source Message must belong to that Conversation |
| Knowledge ingest/upload/detail/delete, retrieval/evaluation/chunk expansion | Bound Workspace and scoped Document/chunk queries; Knowledge tools derive scope from the persisted Run, never model arguments |
| Agent/Tool/Skill configuration | Shared read surface; Agent/Tool mutation is trusted-local only |

Memory `user_id`/`project_id`, Memory/effect `actor` labels and Task State artifact
references are metadata, not authenticated principals or permission grants.
Free-text references do not bypass the scoped resource endpoints when resolved.
Source-only Memory creation must provide `conversation_id` or `run_id`; absent or
foreign sources return the same 404. Own but inconsistent Run/Conversation links
return 400. Standalone explicit Memory remains supported.

SSE uses one write per frame so revocation cannot splice an error inside an event.
The shared delivery guard covers Chat, Continue, Resume and event observation;
keep-alive writes also recheck access. It performs fresh database checks per write
without an authorization cache. Idle event observers recheck on their 15-second
keep-alive. A denied observer receives the existing structured `error` contract:
`unauthenticated`, `not_found` or `service_unavailable`. The browser does not retry
permanent access denial; unauthenticated streams trigger the standard sign-in path.

## Repeatable Evidence

Use Go 1.26.5 and a dedicated `TEST_DATABASE_URL` with CREATEDB/pgvector support.
The test helpers create/drop disposable databases, not the application database.
From `apps/api`:

```bash
go test ./internal/httpapi ./app \
  -run 'TestMemoryProvenance|TestResourceStream|TestAuthenticatedResourceObject|TestMembershipOnlyAdapter|TestObservableStore' \
  -count=1 -v
```

From the repository root:

```bash
AGENTFLOW_IDENTITY_TEST=1 bash scripts/test-browser.sh resource-authorization.spec.ts
```

The two browser cases run real Next.js/Go composition with signed OIDC and
Postgres: Chat creates durable records; independent identities and same-owner
spaces exercise object denial, retrieval isolation, unchanged persisted state,
reload and active-stream logout/continuity. `resource-authorization-evidence.json`
and `stream-authorization-evidence.json` are Playwright attachments retained in
the ignored `apps/web/test-results/results.json` and HTML report, also collected
by CI. No screenshots, passwords or real provider credentials are collected.

Backend integration seeds populated Artifact/effect/full-Capture records and
checks wrong-parent access, all Run command denial, provenance consistency,
session expiry, deletion, Membership removal, changed session identity and actual
SQL/storage failure. It asserts durable revisions/effect versions, not just HTTP
status. Existing Knowledge tool boundary tests cover scoped reads and expansion.

These are deterministic protocol/authorization checks, not a live IdP security
audit, retrieval quality measurement, performance claim or proof of OS isolation.
New resource endpoints and runtime capabilities must extend this matrix and gate.
The approach follows [OWASP authorization guidance](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html).
